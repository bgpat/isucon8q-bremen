package main

import (
	"database/sql"
	"os"
	"os/exec"
	"strconv"

	"github.com/labstack/echo"
)

func registerRoutes(e *echo.Echo) {
	e.GET("/", getRoot, fillinUser)
	e.GET("/initialize", getInitialize)
	e.POST("/api/users", postAPIUsers)
	e.GET("/api/users/:id", getAPIUser, loginRequired)
	e.POST("/api/actions/login", postActionsLogin)
	e.POST("/api/actions/logout", postActionsLogout, loginRequired)
	e.GET("/api/events", getAPIEvents)
	e.GET("/api/events/:id", getAPIEvent)
	e.POST("/api/events/:id/actions/reserve", postReserve, loginRequired)
	e.DELETE("/api/events/:id/sheets/:rank/:num/reservation", deleteReservation, loginRequired)
	registerAdminRoutes(e)
}

func getRoot(c echo.Context) error {
	events, err := getEvents(false)
	if err != nil {
		return err
	}
	for i, v := range events {
		events[i] = sanitizeEvent(v)
	}
	return c.Render(200, "index.tmpl", echo.Map{
		"events": events,
		"user":   c.Get("user"),
		"origin": c.Scheme() + "://" + c.Request().Host,
	})
}

func getInitialize(c echo.Context) error {
	cmd := exec.Command("../../db/init.sh")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	if err != nil {
		return nil
	}

	return c.NoContent(204)
}

func postAPIUsers(c echo.Context) error {
	var params struct {
		Nickname  string `json:"nickname"`
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	c.Bind(&params)

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	var user User
	if err := tx.QueryRow("SELECT * FROM users WHERE login_name = ?", params.LoginName).Scan(&user.ID, &user.LoginName, &user.Nickname, &user.PassHash); err != sql.ErrNoRows {
		tx.Rollback()
		if err == nil {
			return resError(c, "duplicated", 409)
		}
		return err
	}

	res, err := tx.Exec("INSERT INTO users (login_name, pass_hash, nickname) VALUES (?, SHA2(?, 256), ?)", params.LoginName, params.Password, params.Nickname)
	if err != nil {
		tx.Rollback()
		return resError(c, "", 0)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		tx.Rollback()
		return resError(c, "", 0)
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	return c.JSON(201, echo.Map{
		"id":       userID,
		"nickname": params.Nickname,
	})
}

func getAPIUser(c echo.Context) error {
	var user User
	if err := db.QueryRow("SELECT id, nickname FROM users WHERE id = ?", c.Param("id")).Scan(&user.ID, &user.Nickname); err != nil {
		return err
	}

	loginUser, err := getLoginUser(c)
	if err != nil {
		return err
	}
	if user.ID != loginUser.ID {
		return resError(c, "forbidden", 403)
	}

	rows, err := db.Query("SELECT r.*, s.rank AS sheet_rank, s.num AS sheet_num FROM reservations r INNER JOIN sheets s ON s.id = r.sheet_id WHERE r.user_id = ? ORDER BY IFNULL(r.canceled_at, r.reserved_at) DESC LIMIT 5", user.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var recentReservations []Reservation
	for rows.Next() {
		var reservation Reservation
		var sheet Sheet
		if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &sheet.Rank, &sheet.Num); err != nil {
			return err
		}

		event, err := getEvent(reservation.EventID, -1)
		if err != nil {
			return err
		}
		price := event.Sheets[sheet.Rank].Price
		event.Sheets = nil
		event.Total = 0
		event.Remains = 0

		reservation.Event = event
		reservation.SheetRank = sheet.Rank
		reservation.SheetNum = sheet.Num
		reservation.Price = price
		reservation.ReservedAtUnix = reservation.ReservedAt.Unix()
		if reservation.CanceledAt != nil {
			reservation.CanceledAtUnix = reservation.CanceledAt.Unix()
		}
		recentReservations = append(recentReservations, reservation)
	}
	if recentReservations == nil {
		recentReservations = make([]Reservation, 0)
	}

	var totalPrice int
	if err := db.QueryRow("SELECT IFNULL(SUM(e.price + s.price), 0) FROM reservations r INNER JOIN sheets s ON s.id = r.sheet_id INNER JOIN events e ON e.id = r.event_id WHERE r.user_id = ? AND r.canceled_at IS NULL", user.ID).Scan(&totalPrice); err != nil {
		return err
	}

	rows, err = db.Query("SELECT event_id FROM reservations WHERE user_id = ? GROUP BY event_id ORDER BY MAX(IFNULL(canceled_at, reserved_at)) DESC LIMIT 5", user.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var recentEvents []*Event
	for rows.Next() {
		var eventID int64
		if err := rows.Scan(&eventID); err != nil {
			return err
		}
		event, err := getEvent(eventID, -1)
		if err != nil {
			return err
		}
		for k := range event.Sheets {
			event.Sheets[k].Detail = nil
		}
		recentEvents = append(recentEvents, event)
	}
	if recentEvents == nil {
		recentEvents = make([]*Event, 0)
	}

	return c.JSON(200, echo.Map{
		"id":                  user.ID,
		"nickname":            user.Nickname,
		"recent_reservations": recentReservations,
		"total_price":         totalPrice,
		"recent_events":       recentEvents,
	})
}

func postActionsLogin(c echo.Context) error {
	var params struct {
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	c.Bind(&params)

	user := new(User)
	if err := db.QueryRow("SELECT * FROM users WHERE login_name = ?", params.LoginName).Scan(&user.ID, &user.LoginName, &user.Nickname, &user.PassHash); err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "authentication_failed", 401)
		}
		return err
	}

	var passHash string
	if err := db.QueryRow("SELECT SHA2(?, 256)", params.Password).Scan(&passHash); err != nil {
		return err
	}
	if user.PassHash != passHash {
		return resError(c, "authentication_failed", 401)
	}

	sessSetUserID(c, user.ID)
	var err error
	user, err = getLoginUser(c)
	if err != nil {
		return err
	}
	return c.JSON(200, user)
}

func postActionsLogout(c echo.Context) error {
	sessDeleteUserID(c)
	return c.NoContent(204)
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
