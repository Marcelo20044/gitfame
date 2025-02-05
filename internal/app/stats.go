package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"gitfame/configs"
	"golang.org/x/sync/errgroup"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
)

const (
	CommitLen     = 40
	CommitLineLen = 46
)

type Stats struct {
	Name       string `json:"name"`
	Lines      int    `json:"lines"`
	Commits    int    `json:"commits"`
	Files      int    `json:"files"`
	commitsMap map[string]struct{}
}

type StatsCollector struct {
	configs.Config
	Stats    []Stats
	statsMap map[string]*Stats
	mu       sync.Mutex
}

func NewStatsCollector(config configs.Config) *StatsCollector {
	return &StatsCollector{
		Config:   config,
		statsMap: make(map[string]*Stats),
	}
}

// CollectStats collects git statistics, processes each repository file in a separate goroutine. Result saves in Stats list of StatsCollector
func (sc *StatsCollector) CollectStats() error {
	cmd := exec.Command("git", "ls-tree", "-r", sc.Config.Revision, "--name-only")
	cmd.Dir = sc.Config.RepoPath

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list files in repository: %w", err)
	}

	files := strings.Split(strings.TrimSpace(string(output)), "\n")
	filteredFiles := sc.filterFiles(files)

	eg := errgroup.Group{}
	for _, file := range filteredFiles {
		eg.Go(func() error {
			if err = sc.processFile(file); err != nil {
				return fmt.Errorf("failed to process file %s: %w", file, err)
			}
			return nil
		})
	}

	if err = eg.Wait(); err != nil {
		return err
	}

	for _, stat := range sc.statsMap {
		stat.Commits = len(stat.commitsMap)
		sc.Stats = append(sc.Stats, *stat)
	}

	return nil
}

// PrintStats prints collected in CollectStats git statistics in different formats, depending on Format from Config
func (sc *StatsCollector) PrintStats() error {
	sc.sortStats()

	var err error
	switch sc.Config.Format {
	case "tabular":
		err = sc.printTabular(sc.Stats)
	case "csv":
		err = sc.printCSV(sc.Stats)
	case "json":
		err = sc.printJSON(sc.Stats)
	case "json-lines":
		err = sc.printJSONLines(sc.Stats)
	}

	if err != nil {
		return fmt.Errorf("failed to print stats in %s format: %w", sc.Config.Format, err)
	}

	return nil
}

// filterFiles filters out files for collecting depending on Extensions, Excludes and RestrictTo filters from Config
func (sc *StatsCollector) filterFiles(files []string) []string {
	var filteredFiles []string
	for _, file := range files {
		if file != "" &&
			sc.hasExtension(file) &&
			!sc.matchesAnyPattern(file, sc.Config.Excludes) &&
			(len(sc.Config.RestrictTo) == 0 || sc.matchesAnyPattern(file, sc.Config.RestrictTo)) {
			filteredFiles = append(filteredFiles, file)
		}
	}

	return filteredFiles
}

// matchesAnyPattern is a helper function for filterFiles that finds matches between file and glob patterns
func (sc *StatsCollector) matchesAnyPattern(file string, patterns []string) bool {
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, file)
		if err == nil && matched {
			return true
		}

		if strings.HasPrefix(file, strings.TrimSuffix(pattern, "/*")) {
			return true
		}
	}
	return false
}

// hasExtension is a helper function for filterFiles that says if file has extension from Extensions lists from Config
func (sc *StatsCollector) hasExtension(file string) bool {
	if len(sc.Config.Extensions) == 0 {
		return true
	}

	for _, ext := range sc.Config.Extensions {
		if strings.HasSuffix(file, ext) {
			return true
		}
	}
	return false
}

// processFile collects git statistics for file via git blame command. Result saves in statsMap and commitsMap of Stats
func (sc *StatsCollector) processFile(file string) error {
	cmd := exec.Command("git", "blame", "--porcelain", sc.Config.Revision, "--", file)
	cmd.Dir = sc.Config.RepoPath

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to run git blame: %w", err)
	}

	commits := make(map[string]string)
	authors := make(map[string]struct{})
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for i, line := range lines {
		if sc.isCommitLine(line) {
			var author string
			commitInfo := strings.Split(line, " ")
			commitHash := commitInfo[0]

			if auth, exist := commits[commitHash]; !exist {
				author = sc.getAuthor(lines[i:])
				commits[commitHash] = author

				sc.mu.Lock()
				if _, exists := sc.statsMap[author]; !exists {
					sc.statsMap[author] = &Stats{Name: author, commitsMap: make(map[string]struct{})}
				}
				sc.statsMap[author].commitsMap[commitHash] = struct{}{}
				sc.mu.Unlock()

			} else {
				author = auth
			}

			authors[author] = struct{}{}

			var commitLinesCount int
			if commitLinesCount, err = strconv.Atoi(commitInfo[3]); err != nil {
				return fmt.Errorf("failed to parse commit line count %s: %w", line, err)
			}

			sc.mu.Lock()
			sc.statsMap[author].Lines += commitLinesCount
			sc.mu.Unlock()
		}
	}

	// if git blame gave no info (file is empty) but we want to define the creator of the file
	if len(authors) == 0 {
		logCmd := exec.Command("git", "log", sc.Config.Revision, "--", file)
		logCmd.Dir = sc.Config.RepoPath

		output, err = logCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to run git log: %w", err)
		}

		lines = strings.Split(strings.TrimSpace(string(output)), "\n")
		createCommit := strings.Split(lines[0], " ")[1]
		fileAuthor := strings.TrimPrefix(lines[1], "Author: ")
		fileAuthor = fileAuthor[:strings.Index(fileAuthor, "<")-1]

		sc.mu.Lock()
		if _, exists := sc.statsMap[fileAuthor]; !exists {
			sc.statsMap[fileAuthor] = &Stats{Name: fileAuthor, commitsMap: make(map[string]struct{})}
		}
		sc.statsMap[fileAuthor].commitsMap[createCommit] = struct{}{}
		sc.statsMap[fileAuthor].Files++
		sc.mu.Unlock()
	}

	for author := range authors {
		sc.mu.Lock()
		sc.statsMap[author].Files++
		sc.mu.Unlock()
	}

	return nil
}

// isCommitLine is a helper function for processFile that determines whether the output line of git blame command
// contains information about commit
func (sc *StatsCollector) isCommitLine(line string) bool {
	if len(line) < CommitLineLen || len(strings.Split(line, " ")) < 4 {
		return false
	}

	for _, c := range line[:CommitLen] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}

	return true
}

// getAuthor returns the author or commiter of the commit, depending on the UseCommiter config
//
// - lines: []string - a list of lines that starts with the target commit line
func (sc *StatsCollector) getAuthor(lines []string) string {
	if sc.Config.UseCommitter {
		for _, line := range lines {
			if strings.HasPrefix(line, "committer") {
				return strings.TrimPrefix(line, "committer ")
			}
		}
	}

	return strings.TrimPrefix(lines[1], "author ")
}

// sortStats sorts statistics depending on OrderBy param of Config. Operates on the Stats list
func (sc *StatsCollector) sortStats() {
	slices.SortStableFunc(sc.Stats, func(i, j Stats) int {
		compare := func(fields ...func(s Stats) int) int {
			for _, field := range fields {
				if cmp := field(j) - field(i); cmp != 0 {
					return cmp
				}
			}
			return 0
		}

		switch sc.Config.OrderBy {
		case "lines":
			if cmp := compare(func(s Stats) int { return s.Lines }, func(s Stats) int { return s.Commits }, func(s Stats) int { return s.Files }); cmp != 0 {
				return cmp
			}
		case "commits":
			if cmp := compare(func(s Stats) int { return s.Commits }, func(s Stats) int { return s.Lines }, func(s Stats) int { return s.Files }); cmp != 0 {
				return cmp
			}
		case "files":
			if cmp := compare(func(s Stats) int { return s.Files }, func(s Stats) int { return s.Lines }, func(s Stats) int { return s.Commits }); cmp != 0 {
				return cmp
			}
		}

		return strings.Compare(i.Name, j.Name)
	})
}

// printTabular outputs statistics as a tabular-separated table
func (sc *StatsCollector) printTabular(stats []Stats) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)

	if _, err := fmt.Fprintln(w, "Name\tLines\tCommits\tFiles"); err != nil {
		return fmt.Errorf("failed to print header: %w", err)
	}

	for _, stat := range stats {
		if _, err := fmt.Fprintf(w, "%s\t%d\t%d\t%d\n", stat.Name, stat.Lines, stat.Commits, stat.Files); err != nil {
			return fmt.Errorf("failed to print stats line: %w", err)
		}
	}

	err := w.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	return nil
}

// printCSV outputs statistics as a comma-separated table
func (sc *StatsCollector) printCSV(stats []Stats) error {
	w := csv.NewWriter(os.Stdout)

	if err := w.Write([]string{"Name", "Lines", "Commits", "Files"}); err != nil {
		return fmt.Errorf("failed to print header: %w", err)
	}

	for _, stat := range stats {
		if err := w.Write([]string{stat.Name, fmt.Sprintf("%d", stat.Lines), fmt.Sprintf("%d", stat.Commits), fmt.Sprintf("%d", stat.Files)}); err != nil {
			return fmt.Errorf("failed to print stats: %w", err)
		}
	}

	w.Flush()
	return nil
}

// printCSV outputs statistics as a JSON array in one line
func (sc *StatsCollector) printJSON(stats []Stats) error {
	data, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	fmt.Println(string(data))

	return nil
}

// printCSV outputs statistics as a set of JSON objects - each on a new line
func (sc *StatsCollector) printJSONLines(stats []Stats) error {
	for _, stat := range stats {
		data, err := json.Marshal(stat)
		if err != nil {
			return fmt.Errorf("failed to marshal to JSON: %w", err)
		}

		fmt.Println(string(data))
	}

	return nil
}
