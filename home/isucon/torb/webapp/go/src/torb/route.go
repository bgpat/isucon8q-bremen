package main

import (
	"database/sql"
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
