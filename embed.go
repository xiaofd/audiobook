package main

import (
	"embed"
)

//go:embed all:cmd/web/dist
var assets embed.FS