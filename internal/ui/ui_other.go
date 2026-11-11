//go:build !windows

package ui

import (
	"errors"

	"xvoice/internal/session"
)

func SingleInstance() bool { return true }

func Alert(message string) {}

func Run(o Options) error { return errors.New("当前平台尚未实现界面") }

func Update(s session.State) {}
