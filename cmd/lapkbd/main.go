// lapkbd は、外付けキーボードを繋いでいる間だけノート PC の内蔵キーボードを無効にする常駐ツール。
// 振る舞いの正本は SPEC.md。main は cli.Run に任せて、その戻り値で終わるだけ。
package main

import (
	"os"

	"github.com/hidetzu/lapkbd/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
