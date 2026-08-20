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
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yyl1212/agent-studio-node-index/internal/indexgen"
)

var candidatePathPattern = regexp.MustCompile(`^packages/[0-9a-f]{64}\.json$`)

const indexCheckLimit = 30 * time.Second

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
	commandContext, cancel := context.WithTimeout(ctx, indexCheckLimit)
	defer cancel()
	if err := commandContext.Err(); err != nil {
		return err
	}

	rootPath, err := validateCandidateRoot(*root)
	if err != nil {
		return err
	}
	changedPaths, err := canonicalChangedPaths(changed)
	if err != nil {
		return err
	}
	allPaths, submissions, err := preflightCandidateAggregate(commandContext, rootPath)
	if err != nil {
		return err
	}
	paths := changedPaths
	if len(paths) == 0 {
		paths = allPaths
	} else {
		for _, relativePath := range paths {
			if _, exists := submissions[relativePath]; !exists {
				_, err := readCandidateSubmission(rootPath, relativePath)
				if err != nil {
					return err
				}
				return fmt.Errorf("%s is not part of the candidate corpus", relativePath)
			}
		}
	}

	verifier := newVerifier(http.DefaultClient, getenv("GITHUB_TOKEN"))
	for _, relativePath := range paths {
		if err := commandContext.Err(); err != nil {
			return err
		}
		if err := verifier.Verify(commandContext, submissions[relativePath]); err != nil {
			return fmt.Errorf("%s: verification failed: %w", relativePath, err)
		}
	}
	return nil
}

func canonicalChangedPaths(values []string) ([]string, error) {
	paths := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if filepath.ToSlash(value) != value || pathpkg.Clean(value) != value || !candidatePathPattern.MatchString(value) {
			return nil, errors.New("changed file must match packages/<sha256>.json")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		paths = append(paths, value)
	}
	sort.Strings(paths)
	return paths, nil
}

func preflightCandidateAggregate(ctx context.Context, root string) ([]string, map[string]indexgen.Submission, error) {
	paths, err := allCandidatePaths(root)
	if err != nil {
		return nil, nil, err
	}
	if len(paths) > indexgen.MaxPackages*indexgen.MaxVersionsPerPackage {
		return nil, nil, fmt.Errorf("candidate corpus contains too many submission files")
	}

	submissions := make(map[string]indexgen.Submission, len(paths))
	files := make([]indexgen.SubmissionFile, 0, len(paths))
	preflightTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	preflightCommit := strings.Repeat("0", 40)
	for _, relativePath := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		submission, err := readCandidateSubmission(root, relativePath)
		if err != nil {
			return nil, nil, err
		}
		submissions[relativePath] = submission
		files = append(files, indexgen.SubmissionFile{
			Path:        relativePath,
			Submission:  submission,
			IndexCommit: preflightCommit,
			ReviewedAt:  preflightTime,
		})
	}

	index, err := indexgen.Generate(indexgen.GenerateInput{
		Release:      "v0.0.0",
		SourceCommit: preflightCommit,
		GeneratedAt:  preflightTime,
		Submissions:  files,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("candidate aggregate is invalid: %w", err)
	}
	if _, err := indexgen.Encode(index); err != nil {
		return nil, nil, fmt.Errorf("candidate aggregate is invalid: %w", err)
	}
	return paths, submissions, nil
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
