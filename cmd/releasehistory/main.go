package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yyl1212/agent-studio-node-index/internal/releasehistory"
)

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: releasehistory candidates|previous")
	}

	switch args[0] {
	case "candidates":
		flags := flag.NewFlagSet("candidates", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		max := flags.Int("max", 0, "maximum stable annotated Tags")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *max <= 0 {
			return fmt.Errorf("usage: releasehistory candidates --max N")
		}
		tags, err := releasehistory.StableAnnotatedTags(stdin, *max)
		if err != nil {
			return err
		}
		for _, tag := range tags {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", tag.Name, tag.Object, tag.Commit); err != nil {
				return fmt.Errorf("write candidate Tag: %w", err)
			}
		}
		return nil

	case "previous":
		flags := flag.NewFlagSet("previous", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		current := flags.String("current", "", "current stable Release")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *current == "" {
			return fmt.Errorf("usage: releasehistory previous --current VERSION")
		}
		tag, ok, err := releasehistory.HighestPrevious(stdin, *current)
		if err != nil {
			return err
		}
		if ok {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", tag.Name, tag.Object, tag.Commit); err != nil {
				return fmt.Errorf("write previous Release: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
