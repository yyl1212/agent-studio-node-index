package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yyl1212/agent-studio-node-index/internal/indexgen"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("indexgen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "tag checkout root")
	release := flags.String("release", "", "stable index release")
	sourceCommit := flags.String("source-commit", "", "tag checkout commit")
	generatedAtText := flags.String("generated-at", "", "generation timestamp")
	out := flags.String("out", "", "new output directory")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid indexgen flags")
	}
	if flags.NArg() != 0 {
		return errors.New("indexgen does not accept positional arguments")
	}
	for name, value := range map[string]string{
		"-root":          *root,
		"-release":       *release,
		"-source-commit": *sourceCommit,
		"-generated-at":  *generatedAtText,
		"-out":           *out,
	} {
		if value == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	generatedAt, err := time.Parse(time.RFC3339, *generatedAtText)
	if err != nil {
		return errors.New("-generated-at must be an RFC3339 timestamp")
	}
	if _, err := os.Lstat(*out); err == nil {
		return errors.New("output directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("output directory cannot be inspected")
	}

	submissions, err := indexgen.LoadSubmissions(*root)
	if err != nil {
		return err
	}
	schema, err := os.ReadFile(filepath.Join(*root, "schema", indexgen.IndexSchemaAssetName))
	if err != nil {
		return fmt.Errorf("read index schema: %w", err)
	}
	assets, err := indexgen.BuildReleaseAssets(indexgen.ReleaseInput{
		GenerateInput: indexgen.GenerateInput{
			Release:      *release,
			SourceCommit: *sourceCommit,
			GeneratedAt:  generatedAt,
			Submissions:  submissions,
		},
		IndexSchema: schema,
	})
	if err != nil {
		return err
	}

	if err := os.Mkdir(*out, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeAsset(filepath.Join(*out, name), assets[name]); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func writeAsset(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, err := file.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
