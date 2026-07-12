package app

import "embed"

//go:embed templates/*.html static/*
var pageFiles embed.FS
