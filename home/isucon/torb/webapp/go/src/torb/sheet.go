package main

import "time"

type Sheets struct {
	Total   int      `json:"total"`
	Remains int      `json:"remains"`
	Detail  []*Sheet `json:"detail,omitempty"`
	Price   int64    `json:"price"`
}

type Sheet struct {
	ID    int64  `json:"-"`
	Rank  string `json:"-"`
	Num   int64  `json:"num"`
	Price int64  `json:"-"`

	Mine           bool       `json:"mine,omitempty"`
	Reserved       bool       `json:"reserved,omitempty"`
	ReservedAt     *time.Time `json:"-"`
	ReservedAtUnix int64      `json:"reserved_at,omitempty"`
}

var sheetMap = map[string]Sheet{
	"A": Sheet{
		Rank:  "A",
		Num:   150,
		Price: 3000,
	},
	"B": Sheet{
		Rank:  "B",
		Num:   300,
		Price: 1000,
	},
	"C": Sheet{
		Rank:  "C",
		Num:   500,
		Price: 0,
	},
	"S": Sheet{
		Rank:  "S",
		Num:   50,
		Price: 5000,
	},
}
