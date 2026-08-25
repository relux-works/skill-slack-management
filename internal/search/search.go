package search

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Options struct {
	Root       string
	Query      string
	IgnoreCase bool
	Limit      int
	Scopes     []string
}

type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func DefaultScopes() []string {
	return []string{"README.md", "AGENTS.md", "SKILL.md", "references", "testdata"}
}

func Search(opts Options) ([]Match, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = "."
	}
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if len(opts.Scopes) == 0 {
		opts.Scopes = DefaultScopes()
	}

	needle := query
	if opts.IgnoreCase {
		needle = strings.ToLower(query)
	}

	var matches []Match
	for _, scope := range opts.Scopes {
		if len(matches) >= opts.Limit {
			break
		}
		scopePath := filepath.Join(root, scope)
		info, err := os.Stat(scopePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat scope %s: %w", scopePath, err)
		}

		if info.IsDir() {
			err = filepath.WalkDir(scopePath, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					if strings.HasPrefix(d.Name(), ".") && path != scopePath {
						return filepath.SkipDir
					}
					return nil
				}
				found, err := searchFile(path, root, needle, opts.IgnoreCase, opts.Limit-len(matches))
				if err != nil {
					return err
				}
				matches = append(matches, found...)
				if len(matches) >= opts.Limit {
					return errLimitReached
				}
				return nil
			})
			if err != nil && err != errLimitReached {
				return nil, err
			}
			continue
		}

		found, err := searchFile(scopePath, root, needle, opts.IgnoreCase, opts.Limit-len(matches))
		if err != nil {
			return nil, err
		}
		matches = append(matches, found...)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path == matches[j].Path {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Path < matches[j].Path
	})

	if len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}
	return matches, nil
}

var errLimitReached = fmt.Errorf("limit reached")

func searchFile(path, root, needle string, ignoreCase bool, remaining int) ([]Match, error) {
	if remaining <= 0 {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}

	var matches []Match
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		text := scanner.Text()
		haystack := text
		if ignoreCase {
			haystack = strings.ToLower(text)
		}
		if strings.Contains(haystack, needle) {
			matches = append(matches, Match{
				Path: relPath,
				Line: lineNo,
				Text: text,
			})
			if len(matches) >= remaining {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return matches, nil
}
