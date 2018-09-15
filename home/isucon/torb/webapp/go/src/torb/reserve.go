package main

import (
	"database/sql"
	"fmt"
	"go.opencensus.io/trace"
	"log"
	"strconv"
	"time"

	"github.com/labstack/echo"
)

type Reservation struct {
	ID         int64      `json:"id"`
	EventID    int64      `json:"-"`
	SheetID    int64      `json:"-"`
	UserID     int64      `json:"-"`
	ReservedAt *time.Time `json:"-"`
	CanceledAt *time.Time `json:"-"`

	Event          *Event `json:"event,omitempty"`
	SheetRank      string `json:"sheet_rank,omitempty"`
	SheetNum       int64  `json:"sheet_num,omitempty"`
	Price          int64  `json:"price,omitempty"`
	ReservedAtUnix int64  `json:"reserved_at,omitempty"`
	CanceledAtUnix int64  `json:"canceled_at,omitempty"`
}

var reserveIDKey = "rid"

func reserveKey(eventID int64, rank string) string {
	return fmt.Sprintf("r_%v_%v", eventID, rank)
}

func postReserve(c echo.Context) error {
	ctx := c.Request().Context()
	ctx, span := trace.StartSpan(ctx, "postReserve")
	defer span.End()
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}
	var params struct {
		Rank string `json:"sheet_rank"`
	}
	c.Bind(&params)

	user, err := getLoginUser(c)
	if err != nil {
		return err
	}

	event, err := getEvent(ctx, eventID, user.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "invalid_event", 404)
		}
		return err
	} else if !event.PublicFg {
		return resError(c, "invalid_event", 404)
	}

	if !validateRank(params.Rank) {
		return resError(c, "invalid_rank", 400)
	}

	var sheet Sheet
	var reservationID int64
	for {
		s, err := client.HGetAll(reserveKey(event.ID, params.Rank)).Result()
		if err != nil {
			return err
		}
		if sheetMap[params.Rank].Num-int64(len(s)) == 0 {
			return resError(c, "sold_out", 409)
		}

		if err := db.QueryRowContext(ctx, "SELECT * FROM sheets WHERE id NOT IN (SELECT sheet_id FROM reservations WHERE event_id = ? AND canceled_at IS NULL FOR UPDATE) AND `rank` = ? ORDER BY RAND() LIMIT 1", event.ID, params.Rank).Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "sold_out", 409)
			}
			return err
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}

		{
			reservationID, err = client.Incr(reserveIDKey).Result()
			if err != nil {
				log.Println("failed to incr:", err)
			}
		}

		now := time.Now().UTC()
		{
			client.HSet(reserveKey(event.ID, sheet.Rank), strconv.Itoa(int(sheet.Num)), now.Unix())
		}

		_, err = tx.ExecContext(ctx, "INSERT INTO reservations (id, event_id, sheet_id, user_id, reserved_at) VALUES (?, ?, ?, ?, ?)", reservationID, event.ID, sheet.ID, user.ID, now.Format("2006-01-02 15:04:05.000000"))
		if err != nil {
			tx.Rollback()
			log.Println("re-try: rollback by", err)
			continue
		}
		if err != nil {
			tx.Rollback()
			log.Println("re-try: rollback by", err)
			continue
		}
		if err := tx.Commit(); err != nil {
			tx.Rollback()
			log.Println("re-try: rollback by", err)
			continue
		}

		break
	}
	return c.JSON(202, echo.Map{
		"id":         reservationID,
		"sheet_rank": params.Rank,
		"sheet_num":  sheet.Num,
	})
}

func deleteReservation(c echo.Context) error {
	ctx := c.Request().Context()
	ctx, span := trace.StartSpan(ctx, "deleteReservation")
	defer span.End()
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}
	rank := c.Param("rank")
	num := c.Param("num")

	user, err := getLoginUser(c)
	if err != nil {
		return err
	}

	event, err := getEvent(ctx, eventID, user.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "invalid_event", 404)
		}
		return err
	} else if !event.PublicFg {
		return resError(c, "invalid_event", 404)
	}

	if !validateRank(rank) {
		return resError(c, "invalid_rank", 404)
	}

	var sheet Sheet
	if err := db.QueryRowContext(ctx, "SELECT * FROM sheets WHERE `rank` = ? AND num = ?", rank, num).Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "invalid_sheet", 404)
		}
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	var reservation Reservation
	if err := tx.QueryRowContext(ctx, "SELECT * FROM reservations WHERE event_id = ? AND sheet_id = ? AND canceled_at IS NULL GROUP BY event_id HAVING reserved_at = MIN(reserved_at) FOR UPDATE", event.ID, sheet.ID).Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt); err != nil {
		tx.Rollback()
		if err == sql.ErrNoRows {
			return resError(c, "not_reserved", 400)
		}
		return err
	}
	if reservation.UserID != user.ID {
		tx.Rollback()
		return resError(c, "not_permitted", 403)
	}

	if _, err := tx.ExecContext(ctx, "UPDATE reservations SET canceled_at = ? WHERE id = ?", time.Now().UTC().Format("2006-01-02 15:04:05.000000"), reservation.ID); err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if err := client.HDel(reserveKey(event.ID, rank), strconv.Itoa(int(sheet.Num))).Err(); err != nil {
		return err
	}
	return c.NoContent(204)
}

func Rank(sheetId int64) (string, int64) {
	if sheetId <= 50 {
		return "S", sheetId
	}
	if sheetId <= 200 {
		return "A", sheetId - 50
	}
	if sheetId <= 500 {
		return "B", sheetId - 200
	}
	return "C", sheetId - 500
}
