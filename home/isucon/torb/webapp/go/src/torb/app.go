package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/bgpat/ocsql"
	"github.com/go-redis/redis"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/middleware"
	"go.opencensus.io/exporter/jaeger"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
)

func sanitizeEvent(e *Event) *Event {
	sanitized := *e
	sanitized.Price = 0
	sanitized.PublicFg = false
	sanitized.ClosedFg = false
	return &sanitized
}

func fillinUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if user, err := getLoginUser(c); err == nil {
			c.Set("user", user)
		}
		return next(c)
	}
}

func validateRank(rank string) bool {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM sheets WHERE `rank` = ?", rank).Scan(&count)
	return count > 0
}

type Renderer struct {
	templates *template.Template
}

func (r *Renderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return r.templates.ExecuteTemplate(w, name, data)
}

var (
	db     *sql.DB
	client *redis.Client
)

func main() {
	exporter, err := jaeger.NewExporter(jaeger.Options{
		Endpoint: "http://isucon-monitor.401.jp:14268",
		Process: jaeger.Process{
			ServiceName: "torb",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	trace.RegisterExporter(exporter)
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.ProbabilitySampler(0.01)})

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		os.Getenv("DB_USER"), os.Getenv("DB_PASS"),
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"),
		os.Getenv("DB_DATABASE"),
	)

	driverName, err := ocsql.Register("mysql", ocsql.WithOptions(ocsql.TraceOptions{
		Query:       true,
		QueryParams: true,
	}))
	if err != nil {
		log.Fatalf("unable to register our ocsql driver: %v\n", err)
	}
	db, err = sql.Open(driverName, dsn)
	if err != nil {
		log.Fatal(err)
	}

	client = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	pong, err := client.Ping().Result()
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	funcs := template.FuncMap{
		"encode_json": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
	}
	e.Renderer = &Renderer{
		templates: template.Must(template.New("").Delims("[[", "]]").Funcs(funcs).ParseGlob("views/*.tmpl")),
	}
	e.Use(echo.WrapMiddleware(func(h http.Handler) http.Handler {
		return &ochttp.Handler{Handler: h}
	}))
	e.Use(session.Middleware(sessions.NewCookieStore([]byte("secret"))))
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{Output: os.Stderr}))
	e.Static("/", "public")
	registerRoutes(e)

	e.Start(":8080")
}

type Report struct {
	ReservationID int64
	EventID       int64
	Rank          string
	Num           int64
	UserID        int64
	SoldAt        string
	CanceledAt    string
	Price         int64
}

func renderReportCSV(c echo.Context, reports []Report) error {
	sort.Slice(reports, func(i, j int) bool { return strings.Compare(reports[i].SoldAt, reports[j].SoldAt) < 0 })

	body := bytes.NewBufferString("reservation_id,event_id,rank,num,price,user_id,sold_at,canceled_at\n")
	for _, v := range reports {
		body.WriteString(fmt.Sprintf("%d,%d,%s,%d,%d,%d,%s,%s\n",
			v.ReservationID, v.EventID, v.Rank, v.Num, v.Price, v.UserID, v.SoldAt, v.CanceledAt))
	}

	c.Response().Header().Set("Content-Type", `text/csv; charset=UTF-8`)
	c.Response().Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
	_, err := io.Copy(c.Response(), body)
	return err
}

func resError(c echo.Context, e string, status int) error {
	if e == "" {
		e = "unknown"
	}
	if status < 100 {
		status = 500
	}
	return c.JSON(status, map[string]string{"error": e})
}
