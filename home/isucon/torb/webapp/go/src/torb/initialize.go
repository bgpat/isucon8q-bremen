package main

import (
	"log"
	"os"
	"os/exec"
	"strconv"

	"github.com/labstack/echo"
)

func getInitialize(c echo.Context) error {
	cmd := exec.Command("../../db/init.sh")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	if err != nil {
		return nil
	}

	{
		client.FlushDB()
		var id int64
		rows, err := db.Query("SELECT id, event_id, sheet_id, reserved_at FROM reservations WHERE canceled_at IS NULL")
		if err != nil {
			log.Fatalf("failed to initialize: %v", err)
		}
		for rows.Next() {
			r := Reservation{}
			rows.Scan(&r.ID, &r.EventID, &r.SheetID, &r.ReservedAt)
			rank, num := Rank(r.SheetID)
			client.HSet(reserveKey(r.EventID, rank), strconv.Itoa(int(num)), r.ReservedAt.Unix())
			if id < r.ID {
				id = r.ID
			}
		}
		client.Set(reserveIDKey, id, 0)
	}

	return c.NoContent(204)
}
