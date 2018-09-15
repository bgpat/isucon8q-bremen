package main

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"go.opencensus.io/trace"

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
	s, err := client.HGetAll(reserveKey(event.ID, params.Rank)).Result()
	if err != nil {
		return err
	}
	if len(s) == int(sheetMap[params.Rank].Num) {
		return resError(c, "sold_out", 409)
	}

	sheet = RandSheet(params.Rank, s)
	if sheet.ID == 0 {
		return resError(c, "sold_out", 409)
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

	go func() {
		_, err = tx.ExecContext(ctx, "INSERT INTO reservations (id, event_id, sheet_id, user_id, reserved_at) VALUES (?, ?, ?, ?, ?)", reservationID, event.ID, sheet.ID, user.ID, now.Format("2006-01-02 15:04:05.000000"))
	}()
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

/*
   rank : SABCのどれか
   s : keyがすでに使われているNumをstringにしたもの
*/
func RandSheet(rank string, s map[string]string) Sheet {
	r := make([]int, 0, sheetMap[rank].Num)
	q := make([]int64, 0, sheetMap[rank].Num)
	for k, _ := range s {
		used, _ := strconv.Atoi(k)
		r = append(r, used)
	}

	r = sort.IntSlice(r)

	j := 0
	for i := int64(1); i <= sheetMap[rank].Num; i++ {
		if j < len(r) && i == int64(r[j]) {
			j++
		} else {
			q = append(q, i)
		}
	}
	sheet := Sheet{}
	sheet.Rank = rank
	sheet.Num = q[rand.Intn(len(q))]
	sheet.ID = sheet.Num
	for _, c := range "SABC" {
		if string(c) == rank {
			break
		}
		sheet.ID += sheetMap[string(c)].Num
	}
	return sheet
}
