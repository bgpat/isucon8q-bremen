package main

import (
	"database/sql"
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

func postReserve(c echo.Context) error {
	ctx := c.Request().Context()
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

		res, err := tx.ExecContext(ctx, "INSERT INTO reservations (event_id, sheet_id, user_id, reserved_at) VALUES (?, ?, ?, ?)", event.ID, sheet.ID, user.ID, time.Now().UTC().Format("2006-01-02 15:04:05.000000"))
		if err != nil {
			tx.Rollback()
			log.Println("re-try: rollback by", err)
			continue
		}
		reservationID, err = res.LastInsertId()
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

	return c.NoContent(204)
}
