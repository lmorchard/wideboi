//go:build !desktop

package main

import "errors"

const desktopBuild = false

func runDesktop() error {
	return errors.New("desktop support is not in this build; use make desktop")
}
