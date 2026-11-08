// Package main 是程序入口：只负责解析参数并交给 cli 包。
package main

import (
	"os"

	"github.com/infinite-galaxy/rainclass-reminder/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
