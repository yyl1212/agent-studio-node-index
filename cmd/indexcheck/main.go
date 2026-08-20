package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yyl1212/agent-studio-node-index/internal/indexgen"
)

var candidatePathPattern = regexp.MustCompile(`^packages/[0-9a-f]{64}\.json$`)

type submissionVerifier interface {
	Verify(context.Context, indexgen.Submission) error
}

type verifierFactory func(*http.Client, string) submissionVerifier

type changedFiles []string

func (files *changedFiles) String() string {
	return strings.Join(*files, ",")
}

func (files *changedFiles) Set(value string) error {
	*files = append(*files, value)
	return nil
}

func main() {
	err := runIndexCheck(context.Background(), os.Args[1:], os.Getenv, func(client *http.Client, token string) submissionVerifier {
		return indexgen.NewGitHubVerifier(client, token)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runIndexCheck(ctx context.Context, args []string, getenv func(string) string, newVerifier verifierFactory) error {
	flags := flag.NewFlagSet("indexcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "candidate checkout root")
	var changed changedFiles
	flags.Var(&changed, "changed-file", "changed package submission path")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid indexcheck flags")
	}
	if flags.NArg() != 0 {
		return errors.New("indexcheck does not accept positional arguments")
	}
	if *root == "" {
		return errors.New("-root is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rootPath, err := validateCandidateRoot(*root)
	if err != nil {
		return err
	}
	paths := []string(changed)
	if len(paths) == 0 {
		paths, err = allCandidatePaths(rootPath)
		if err != nil {
			return err
		}
	}

	verifier := newVerifier(http.DefaultClient, getenv("GITHUB_TOKEN"))
	for _, relativePath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		submission, err := readCandidateSubmission(rootPath, relativePath)
		if err != nil {
			return err
		}
		if err := verifier.Verify(ctx, submission); err != nil {
			return fmt.Errorf("%s: verification failed: %w", relativePath, err)
		}
	}
	return nil
}

func validateCandidateRoot(root string) (string, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("candidate root is invalid")
	}
	info, err := os.Lstat(rootPath)
	if err != nil {
		return "", errors.New("candidate root does not exist")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("candidate root must be a real directory")
	}
	return rootPath, nil
}

func allCandidatePaths(root string) ([]string, error) {
	packagesPath := filepath.Join(root, "packages")
	info, err := os.Lstat(packagesPath)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, errors.New("candidate packages directory cannot be inspected")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("candidate packages directory must be a real directory")
	}
	entries, err := os.ReadDir(packagesPath)
	if err != nil {
		return nil, errors.New("candidate packages directory cannot be read")
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, "packages/"+entry.Name())
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func readCandidateSubmission(root, relativePath string) (indexgen.Submission, error) {
	if filepath.ToSlash(relativePath) != relativePath || !candidatePathPattern.MatchString(relativePath) {
		return indexgen.Submission{}, errors.New("changed file must match packages/<sha256>.json")
	}
	packagesInfo, err := os.Lstat(filepath.Join(root, "packages"))
	if errors.Is(err, os.ErrNotExist) {
		return indexgen.Submission{}, fmt.Errorf("%s does not exist; deleted submissions are rejected", relativePath)
	}
	if err != nil {
		return indexgen.Submission{}, errors.New("candidate packages directory cannot be inspected")
	}
	if !packagesInfo.IsDir() || packagesInfo.Mode()&os.ModeSymlink != 0 {
		return indexgen.Submission{}, errors.New("candidate packages directory must be a real directory")
	}
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return indexgen.Submission{}, fmt.Errorf("%s does not exist; deleted submissions are rejected", relativePath)
	}
	if err != nil {
		return indexgen.Submission{}, fmt.Errorf("%s cannot be inspected", relativePath)
	}
	if !info.Mode().IsRegular() {
		return indexgen.Submission{}, fmt.Errorf("%s must be a regular file", relativePath)
	}
	if info.Size() < 0 || info.Size() > indexgen.MaxSubmissionBytes {
		return indexgen.Submission{}, fmt.Errorf("%s has invalid size", relativePath)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return indexgen.Submission{}, fmt.Errorf("%s cannot be read", relativePath)
	}
	if len(data) > indexgen.MaxSubmissionBytes {
		return indexgen.Submission{}, fmt.Errorf("%s has invalid size", relativePath)
	}
	submission, err := indexgen.ParseSubmission(relativePath, data)
	if err != nil {
		return indexgen.Submission{}, fmt.Errorf("%s is not a valid submission", relativePath)
	}
	if filepath.Base(relativePath) != candidateFilename(submission) {
		return indexgen.Submission{}, fmt.Errorf("%s filename hash does not match name and version", relativePath)
	}
	return submission, nil
}

func candidateFilename(submission indexgen.Submission) string {
	digest := sha256.Sum256([]byte(submission.Name + "\n" + submission.Version))
	return fmt.Sprintf("%x.json", digest)
}
