package main

import (
	"database/sql"
	"strconv"

	"github.com/labstack/echo"
)

type Event struct {
	ID       int64  `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	PublicFg bool   `json:"public,omitempty"`
	ClosedFg bool   `json:"closed,omitempty"`
	Price    int64  `json:"price,omitempty"`

	Total   int                `json:"total"`
	Remains int                `json:"remains"`
	Sheets  map[string]*Sheets `json:"sheets,omitempty"`
}

func getEventsRoot(all bool) ([]*Event, error) {
	rows1, err := db.Query("SELECT id, title, price FROM events WHERE public_fg = 1 ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows1.Close()

	memo := make(map[int64]map[int]int)

	for i, v := range [][]int{
		[]int{0, 50},
		[]int{50, 200},
		[]int{200, 500},
		[]int{500, 1000},
	} {

		rows, err := db.Query("SELECT event_id, count(1) FROM reservations WHERE ? < sheet_id AND sheet_id <= ? AND canceled_at IS NULL GROUP BY event_id", v[0], v[1])
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var id int64
			var count int
			if err := rows.Scan(&id, &count); err != nil {
				return nil, err
			}
			if memo[id] == nil {
				memo[id] = make(map[int]int)
			}
			memo[id][i] = count
			memo[id][4] += count
		}
	}

	var events []*Event
	for rows1.Next() {
		var event Event

		if err := rows1.Scan(&event.ID, &event.Title, &event.Price); err != nil {
			return nil, err
		}

		event.Sheets = map[string]*Sheets{
			"S": &Sheets{Total: 50, Price: 5000 + event.Price, Remains: memo[event.ID][0]},
			"A": &Sheets{Total: 150, Price: 3000 + event.Price, Remains: memo[event.ID][1]},
			"B": &Sheets{Total: 300, Price: 1000 + event.Price, Remains: memo[event.ID][2]},
			"C": &Sheets{Total: 500, Price: 0 + event.Price, Remains: memo[event.ID][3]},
		}
		event.Total = 1000
		event.Remains = memo[event.ID][4]

		events = append(events, &event)
	}
	return events, nil

	/*
		var events []*Event
		for rows.Next() {
			var event Event
			if err := rows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price); err != nil {
				return nil, err
			}
			if !all && !event.PublicFg {
				continue
			}
			events = append(events, &event)
		}
		for i, v := range events {
			event, err := getEvent(v.ID, -1)
			if err != nil {
				return nil, err
			}
			for k := range event.Sheets {
				event.Sheets[k].Detail = nil
			}
			events[i] = event
		}
		return events, nil
	*/
}

func getEvents(all bool) ([]*Event, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Commit()

	rows, err := tx.Query("SELECT * FROM events ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price); err != nil {
			return nil, err
		}
		if !all && !event.PublicFg {
			continue
		}
		events = append(events, &event)
	}
	for i, v := range events {
		event, err := getEvent(v.ID, -1)
		if err != nil {
			return nil, err
		}
		for k := range event.Sheets {
			event.Sheets[k].Detail = nil
		}
		events[i] = event
	}
	return events, nil
}

func getEvent(eventID, loginUserID int64) (*Event, error) {
	var event Event
	if err := db.QueryRow("SELECT * FROM events WHERE id = ?", eventID).Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price); err != nil {
		return nil, err
	}
	event.Sheets = map[string]*Sheets{
		"S": &Sheets{},
		"A": &Sheets{},
		"B": &Sheets{},
		"C": &Sheets{},
	}

	rows, err := db.Query("SELECT * FROM sheets ORDER BY `rank`, num")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sheet Sheet
		if err := rows.Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
			return nil, err
		}
		event.Sheets[sheet.Rank].Price = event.Price + sheet.Price
		event.Total++
		event.Sheets[sheet.Rank].Total++

		var reservation Reservation
		err := db.QueryRow("SELECT * FROM reservations WHERE event_id = ? AND sheet_id = ? AND canceled_at IS NULL GROUP BY event_id, sheet_id HAVING reserved_at = MIN(reserved_at)", event.ID, sheet.ID).Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt)
		if err == nil {
			sheet.Mine = reservation.UserID == loginUserID
			sheet.Reserved = true
			sheet.ReservedAtUnix = reservation.ReservedAt.Unix()
		} else if err == sql.ErrNoRows {
			event.Remains++
			event.Sheets[sheet.Rank].Remains++
		} else {
			return nil, err
		}

		event.Sheets[sheet.Rank].Detail = append(event.Sheets[sheet.Rank].Detail, &sheet)
	}

	return &event, nil
}

func getAPIEvents(c echo.Context) error {
	events, err := getEvents(true)
	if err != nil {
		return err
	}
	for i, v := range events {
		events[i] = sanitizeEvent(v)
	}
	return c.JSON(200, events)
}

func getAPIEvent(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}

	loginUserID := int64(-1)
	if user, err := getLoginUser(c); err == nil {
		loginUserID = user.ID
	}

	event, err := getEvent(eventID, loginUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "not_found", 404)
		}
		return err
	} else if !event.PublicFg {
		return resError(c, "not_found", 404)
	}
	return c.JSON(200, sanitizeEvent(event))
}
