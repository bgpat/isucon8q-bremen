package main

import (
	"os"
	"os/exec"

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

	return c.NoContent(204)
}
