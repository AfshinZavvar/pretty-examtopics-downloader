package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"examtopics-downloader/internal/fetch"
	"examtopics-downloader/internal/utils"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiGray   = "\x1b[90m"
)

var useANSI = detectANSI()

func main() {
	defer func() {
		if r := recover(); r != nil {
			printErrorf("Unexpected error: %v\n", r)
			pauseBeforeExitOnError()
			os.Exit(1)
		}
	}()

	if err := run(); err != nil {
		printErrorf("Error: %v\n", err)
		pauseBeforeExitOnError()
		os.Exit(1)
	}
}

func run() error {
	if wantsHelp(os.Args[1:]) {
		printUsage(os.Stdout)
		return nil
	}

	debug := flag.Bool("debug", false, "Enable debug logs")
	noCache := flag.Bool("no-cache", false, "Bypass provider-index and question-page caches (always fetch fresh)")
	refreshIndex := flag.Bool("refresh-index", false, "Force a fresh provider discussion index while retaining question-page cache entries")
	recentDays := flag.Int("recent-comments-days", 0, "Filter by dated comment activity in the last N days; unknown dates remain")
	flag.Parse()
	fetch.SetDebug(*debug)
	fetch.SetCacheEnabled(!*noCache)

	// Distinguish "flag explicitly passed" from "left at default 0" so we only
	// fall back to the interactive prompt when the user didn't specify a window.
	recentDaysSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "recent-comments-days" {
			recentDaysSet = true
		}
	})

	printBanner()

	reader := bufio.NewReader(os.Stdin)

	selectedProvider, err := promptSelectionWithRefresh(
		reader,
		"Available Providers",
		getProvidersWithStatus,
		formatProviderName,
	)
	if err != nil {
		return fmt.Errorf("failed reading provider selection: %w", err)
	}

	if *refreshIndex {
		fetch.InvalidateProviderDiscussionIndex(selectedProvider)
	}

	selectedExam, err := promptExamSelection(reader, selectedProvider)
	if err != nil {
		return fmt.Errorf("failed reading exam selection: %w", err)
	}
	extractionFilter := selectedExam
	if selectedExam == "all-discussions" {
		extractionFilter = ""
	}

	// Resolve the recent-comment window: an explicit flag wins; otherwise ask.
	recentWindowDays := *recentDays
	if !recentDaysSet {
		recentWindowDays, err = promptRecentCommentsDays(reader)
		if err != nil {
			return fmt.Errorf("failed reading recent-comments window: %w", err)
		}
	}

	printInfof("Starting extraction for %s / %s...\n", formatProviderName(selectedProvider), selectedExam)
	extractionCtx, stopExtraction := signal.NotifyContext(context.Background(), os.Interrupt)
	links, extractionErr := fetch.GetAllPages(extractionCtx, selectedProvider, extractionFilter, newCLIProgressReporter())
	stopExtraction()
	if extractionErr != nil {
		if len(links) == 0 {
			return fmt.Errorf("extraction failed: %w", extractionErr)
		}
		printWarnf("The question set is PARTIAL: %v\n", extractionErr)
		printWarnf("Writing %d recovered question(s). Run again to resume discovery before treating the HTML as complete.\n", len(links))
	}
	if len(links) == 0 {
		return fmt.Errorf("no matching questions were extracted")
	}

	if recentWindowDays > 0 {
		before := len(links)
		links = utils.FilterByRecentComments(links, recentWindowDays, time.Now())
		printInfof("Recent-comment filter (last %d days; unknown dates retained): kept %d, dropped %d of %d question(s).\n",
			recentWindowDays, len(links), before-len(links), before)
		if len(links) == 0 {
			return fmt.Errorf("no questions remained after the %d-day comment-activity filter; rerun with a larger -recent-comments-days value or 0 to disable", recentWindowDays)
		}
	}

	outputPath := defaultOutputPath(selectedProvider, selectedExam)
	if extractionErr != nil {
		outputPath = strings.TrimSuffix(outputPath, ".html") + ".partial.html"
	}
	headerExam := selectedExam
	if selectedExam == "all-discussions" {
		headerExam = ""
	}
	savedFiles, err := utils.WriteDataWithSelection(links, outputPath, true, selectedProvider, headerExam)
	if err != nil {
		return fmt.Errorf("failed writing output: %w", err)
	}

	printSuccessf("Successfully saved output: %s\n", strings.Join(savedFiles, ", "))
	return nil
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "/?", "-?", "-h", "--help", "-help", "help":
			return true
		}
	}
	return false
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "ExamTopics Downloader - Interactive Exam Extractor")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  examtopics-downloader-windows-amd64.exe [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  -debug                    Enable diagnostic logs")
	fmt.Fprintln(w, "  -no-cache                 Bypass provider-index and question-page caches")
	fmt.Fprintln(w, "  -refresh-index            Rebuild the provider index; retain question cache")
	fmt.Fprintln(w, "  -recent-comments-days N   Filter by recent dated comments; unknown dates remain")
	fmt.Fprintln(w, "                             Use 0 to disable the filter")
	fmt.Fprintln(w, "  -h, --help, /?            Show this help and exit")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Interactive exam commands:")
	fmt.Fprintln(w, "  az-305      Find exams containing az-305")
	fmt.Fprintln(w, "  1           Select displayed item 1")
	fmt.Fprintln(w, "  /clear      Show the full list again")
	fmt.Fprintln(w, "  /reload     Reload official exams")
	fmt.Fprintln(w, "  /scan       Resume public discussion discovery (two-minute pass)")
	fmt.Fprintln(w, "  /all        Download indexed public provider discussions (may be partial)")
	fmt.Fprintln(w, "Enter one item at a time, then press Enter.")
}

func pauseBeforeExitOnError() {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return
	}
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		return
	}

	fmt.Print(style("Press Enter to close...", ansiGray))
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func getProvidersWithStatus() []string {
	printInfof("Loading providers from ExamTopics...\n")
	fmt.Println(style("Fetching the exam and discussion indexes in parallel (Ctrl+C cancels).", ansiGray))
	start := time.Now()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	providers, err := fetch.GetAllProvidersContext(ctx)
	stop()

	elapsed := time.Since(start).Round(time.Second)
	if err != nil {
		printWarnf("Provider loading ended after %s: %v\n", elapsed, err)
		return providers
	}
	printSuccessf("Done. Found %d provider(s) in %s.\n", len(providers), elapsed)
	return providers
}

// promptExamSelection keeps the fast official list interactive. Exhaustive
// provider discussion discovery is explicit via /scan and can be cancelled
// without discarding already checkpointed pages.
func promptExamSelection(reader *bufio.Reader, provider string) (string, error) {
	providerLabel := formatProviderName(provider)
	loadOfficial := func() ([]string, error) {
		ctx, cancelTimeout := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancelTimeout()
		ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
		defer stopSignal()
		return fetch.GetOfficialExamSlugs(ctx, provider)
	}

	printSection(fmt.Sprintf("Exam Discovery: %s", providerLabel))
	printInfof("Loading official exams (discussion scan is available later with /scan)...\n")
	official, officialErr := loadOfficial()
	if officialErr != nil {
		printWarnf("Official exam list could not be loaded: %v\n", officialErr)
	} else {
		printSuccessf("Loaded %d official exam(s).\n", len(official))
	}

	cached, cacheHit := fetch.GetCachedProviderExamSlugs(provider)
	if cacheHit {
		printSuccessf("Added %d cached discussion-derived exam(s).\n", len(cached))
	}
	discussion := append([]string(nil), cached...)
	scanDiscussions := func() (fetch.ProviderIndex, error) {
		printInfof("Scanning discussion pages for additional exams. Press Ctrl+C to stop and keep partial results.\n")
		scanCtx, stopScan := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stopScan()
		return fetch.GetProviderDiscussionIndex(scanCtx, provider, fetch.IndexOptions{}, newCLIProgressReporter())
	}
	return promptExamMenu(reader, provider, official, discussion, loadOfficial, scanDiscussions)
}

func promptExamMenu(
	reader *bufio.Reader,
	provider string,
	official []string,
	discussion []string,
	loadOfficial func() ([]string, error),
	scanDiscussions func() (fetch.ProviderIndex, error),
) (string, error) {
	providerLabel := formatProviderName(provider)
	options := fetch.MergeProviderExamSlugs(provider, official, discussion)
	filter := ""

	for {
		all := make([]selectionOption, 0, len(options))
		for i, option := range options {
			all = append(all, selectionOption{RawIndex: i, Label: option})
		}
		filtered := filterOptions(all, filter)
		printMenuHeader(fmt.Sprintf("Available Exams for %s", providerLabel), len(filtered), len(all), filter)
		if len(filtered) == 0 {
			printWarnf("No exam choices are currently listed. Use /scan or /all.\n")
		} else {
			printOptionsInColumns(filtered)
		}
		printExamMenuHelp()
		fmt.Print(style("Exam> ", ansiBold+ansiCyan))

		raw, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		if command, value, isCommand := parseMenuCommand(raw); isCommand {
			switch command {
			case "clear":
				filter = ""
			case "all":
				return "all-discussions", nil
			case "refresh":
				printInfof("Refreshing the official exam list...\n")
				refreshed, refreshErr := loadOfficial()
				if refreshErr != nil {
					printWarnf("Official refresh failed; keeping the current list: %v\n", refreshErr)
				} else {
					official = refreshed
					options = fetch.MergeProviderExamSlugs(provider, official, discussion)
					filter = ""
					printSuccessf("Official list refreshed; %d total option(s).\n", len(options))
				}
			case "scan":
				index, scanErr := scanDiscussions()
				discussion = index.ExamSlugs
				options = fetch.MergeProviderExamSlugs(provider, official, discussion)
				filter = ""
				if scanErr != nil {
					printWarnf("Discussion scan paused at %d/%d pages with %d exam code(s) found: %v\n", len(index.CompletedPages), index.TotalPages, len(index.ExamSlugs), scanErr)
					printInfof("Run /scan again to resume from page %d; completed pages are cached.\n", len(index.CompletedPages)+1)
				} else {
					printSuccessf("Discussion scan complete; %d total exam option(s).\n", len(options))
				}
			case "filter":
				if value == "" {
					printWarnf("Filter text is missing. Example: /filter az-305\n")
				} else {
					filter = value
				}
			}
			continue
		}

		choice, err := strconv.Atoi(raw)
		if err != nil {
			filter = raw
			continue
		}
		if choice < 1 || choice > len(filtered) {
			printWarnf("Invalid selection. Enter a listed number or one of the commands above.\n")
			continue
		}
		return options[filtered[choice-1].RawIndex], nil
	}
}

func parseMenuCommand(raw string) (command string, value string, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return "", "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(raw, "/"))
	if body == "" {
		return "clear", "", true
	}
	fields := strings.Fields(body)
	name := strings.ToLower(fields[0])
	switch name {
	case "refresh", "reload":
		return "refresh", "", true
	case "clear":
		return "clear", "", true
	case "scan", "all":
		return name, "", true
	case "filter", "f":
		return "filter", strings.TrimSpace(body[len(fields[0]):]), true
	default:
		// Preserve the original shorthand where /az-305 meant "filter az-305".
		return "filter", body, true
	}
}

func newCLIProgressReporter() fetch.ProgressFunc {
	lastPrinted := time.Time{}
	lastPhase := ""
	lastCompleted := -1
	return func(progress fetch.Progress) {
		if progress.FromCache {
			if progress.Phase != lastPhase {
				printSuccessf("Loaded cached discussion index: %d page(s), %d exam(s), %d cached link(s).\n",
					progress.Total, progress.Found, progress.CacheHits)
				lastPhase = progress.Phase
			}
			return
		}
		now := time.Now()
		if !progress.Complete && progress.Completed == lastCompleted && now.Sub(lastPrinted) < time.Second {
			return
		}
		if !progress.Complete && !lastPrinted.IsZero() && now.Sub(lastPrinted) < time.Second {
			return
		}
		lastPrinted = now
		lastPhase = progress.Phase
		lastCompleted = progress.Completed
		switch progress.Phase {
		case "discussion-index":
			printInfof("Scanning discussion pages %d/%d · %d exam codes found · %d retries · %d failures · %.1f rps · %s\n",
				progress.Completed, progress.Total, progress.Found, progress.Retries, progress.Failures,
				progress.Rate, progress.Elapsed.Round(time.Second))
		case "questions":
			printInfof("Downloading questions %d/%d · %d extracted · %d cache hits · %d retries · %d failures · %s\n",
				progress.Completed, progress.Total, progress.Found, progress.CacheHits,
				progress.Retries, progress.Failures, progress.Elapsed.Round(time.Second))
		}
	}
}

// promptRecentCommentsDays asks for an optional "recent comment activity"
// window in days. Blank input means "keep all" (returns 0). Re-prompts on
// non-numeric or negative input.
func promptRecentCommentsDays(reader *bufio.Reader) (int, error) {
	prompt := "Filter by dated comment activity in the last N days? (180 = ~6 months; blank = no date filter; unknown dates remain): "
	for {
		fmt.Print(style(prompt, ansiBold+ansiCyan))

		raw, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}

		answer := strings.TrimSpace(raw)
		if answer == "" {
			return 0, nil
		}

		days, err := strconv.Atoi(answer)
		if err != nil || days < 0 {
			printWarnf("Please enter a positive number of days, or leave blank to disable the date filter.\n")
			continue
		}
		return days, nil
	}
}

func promptSelectionWithRefresh(
	reader *bufio.Reader,
	title string,
	loadOptions func() []string,
	formatter func(string) string,
) (string, error) {
	options := loadOptions()
	if len(options) == 0 {
		return "", fmt.Errorf("no options found for %s", title)
	}

	filter := ""
	for {
		all := make([]selectionOption, 0, len(options))
		for i, opt := range options {
			all = append(all, selectionOption{
				RawIndex: i,
				Label:    formatter(opt),
			})
		}

		filtered := filterOptions(all, filter)
		printMenuHeader(title, len(filtered), len(all), filter)
		if len(filtered) == 0 {
			printWarnf("No results for filter %q. Use / to clear.\n", filter)
		} else {
			printOptionsInColumns(filtered)
		}
		printMenuHelp()

		fmt.Print(style("Provider> ", ansiBold+ansiCyan))
		raw, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}

		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		if command, value, isCommand := parseMenuCommand(raw); isCommand {
			switch command {
			case "clear":
				filter = ""
			case "refresh":
				printInfof("Refreshing list...\n")
				refreshed := loadOptions()
				if len(refreshed) == 0 {
					printWarnf("Refresh returned no results. Keeping current list.\n")
				} else {
					options = refreshed
					filter = ""
					printSuccessf("List refreshed. %d option(s) available.\n", len(options))
				}
			case "filter":
				if value == "" {
					printWarnf("Filter text is missing. Example: /filter microsoft\n")
				} else {
					filter = value
				}
			default:
				printWarnf("That command is only available in the exam menu.\n")
			}
			continue
		}

		choice, err := strconv.Atoi(raw)
		if err != nil {
			filter = raw
			continue
		}
		if choice < 1 || choice > len(filtered) {
			printWarnf("Invalid selection. Please enter a valid number.\n")
			continue
		}

		return options[filtered[choice-1].RawIndex], nil
	}
}

type selectionOption struct {
	RawIndex int
	Label    string
}

func filterOptions(options []selectionOption, filter string) []selectionOption {
	if strings.TrimSpace(filter) == "" {
		return options
	}

	filter = strings.ToLower(strings.TrimSpace(filter))
	filtered := make([]selectionOption, 0, len(options))
	for _, opt := range options {
		if strings.Contains(strings.ToLower(opt.Label), filter) {
			filtered = append(filtered, opt)
		}
	}
	return filtered
}

func printOptionsInColumns(options []selectionOption) {
	if len(options) == 0 {
		return
	}

	lines := make([]string, 0, len(options))
	maxWidth := 0
	for i, opt := range options {
		line := fmt.Sprintf("%3d. %s", i+1, strings.TrimSpace(opt.Label))
		lines = append(lines, line)
		if len(line) > maxWidth {
			maxWidth = len(line)
		}
	}

	colWidth := maxWidth + 4
	if colWidth < 24 {
		colWidth = 24
	}
	targetWidth := 120
	cols := targetWidth / colWidth
	if cols < 1 {
		cols = 1
	}
	if cols > 4 {
		cols = 4
	}

	rows := int(math.Ceil(float64(len(lines)) / float64(cols)))
	for r := 0; r < rows; r++ {
		var row strings.Builder
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			if idx >= len(lines) {
				continue
			}
			if c > 0 {
				row.WriteString("  ")
			}
			row.WriteString(style(fmt.Sprintf("%-*s", colWidth, lines[idx]), ansiCyan))
		}
		fmt.Println(strings.TrimRight(row.String(), " "))
	}
}

func printBanner() {
	fmt.Println(style(strings.Repeat("=", 64), ansiGray))
	fmt.Println(style(" ExamTopics Downloader - Interactive Exam Extractor", ansiBold+ansiCyan))
	fmt.Println(style(strings.Repeat("=", 64), ansiGray))
	fmt.Println()
}

func printSection(title string) {
	fmt.Println()
	fmt.Println(style(strings.Repeat("-", 64), ansiGray))
	fmt.Println(style(" "+title, ansiBold+ansiCyan))
	fmt.Println(style(strings.Repeat("-", 64), ansiGray))
}

func printMenuHeader(title string, shown int, total int, filter string) {
	printSection(title)
	fmt.Println(style(fmt.Sprintf(" Showing %d of %d", shown, total), ansiGray))
	if strings.TrimSpace(filter) != "" {
		fmt.Println(style(fmt.Sprintf(" Filter: %q", filter), ansiYellow))
	}
	fmt.Println()
}

func printMenuHelp() {
	fmt.Println(style(" Enter one item, then press Enter:", ansiGray))
	fmt.Println(style("   microsoft   Find providers containing microsoft", ansiGray))
	fmt.Println(style("   1           Select displayed item 1", ansiGray))
	fmt.Println(style("   /clear      Show the full provider list again", ansiGray))
	fmt.Println(style("   /reload     Fetch the provider list again", ansiGray))
}

func printExamMenuHelp() {
	fmt.Println(style(" Enter one item, then press Enter:", ansiGray))
	fmt.Println(style("   az-305      Find exams containing az-305", ansiGray))
	fmt.Println(style("   1           Select displayed item 1", ansiGray))
	fmt.Println(style("   /clear      Show the full exam list again", ansiGray))
	fmt.Println(style("   /reload     Fetch the official exam list again", ansiGray))
	fmt.Println(style("   /scan       Find more exams from discussions (up to 2 minutes)", ansiGray))
	fmt.Println(style("   /all        Download every indexed public discussion for this provider", ansiGray))
}

func printInfof(format string, args ...any) {
	fmt.Printf(style("[INFO] ", ansiCyan)+format, args...)
}

func printSuccessf(format string, args ...any) {
	fmt.Printf(style("[OK] ", ansiGreen)+format, args...)
}

func printWarnf(format string, args ...any) {
	fmt.Printf(style("[WARN] ", ansiYellow)+format, args...)
}

func printErrorf(format string, args ...any) {
	fmt.Printf(style("[ERROR] ", ansiRed)+format, args...)
}

func style(text string, code string) string {
	if !useANSI || text == "" {
		return text
	}
	return code + text + ansiReset
}

func detectANSI() bool {
	stat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	if !supportsANSI(runtime.GOOS, (stat.Mode()&os.ModeCharDevice) != 0, os.LookupEnv) {
		return false
	}
	if runtime.GOOS == "windows" {
		return enableVirtualTerminal()
	}
	return true
}

func supportsANSI(goos string, terminal bool, lookupEnv func(string) (string, bool)) bool {
	if !terminal {
		return false
	}
	if _, disabled := lookupEnv("NO_COLOR"); disabled {
		return false
	}
	termValue, _ := lookupEnv("TERM")
	term := strings.TrimSpace(strings.ToLower(termValue))
	if term == "dumb" {
		return false
	}
	_ = goos
	return true
}

func defaultOutputPath(provider, examSlug string) string {
	baseProvider := sanitizeFilenameSegment(provider)
	baseExamCode := sanitizeFilenameSegment(examSlug)
	if baseProvider == "" {
		baseProvider = "examtopics"
	}
	if baseExamCode == "" {
		baseExamCode = "output"
	}

	return fmt.Sprintf("%s_%s.html", baseProvider, baseExamCode)
}

func sanitizeFilenameSegment(input string) string {
	segment := strings.TrimSpace(strings.ToLower(input))
	if segment == "" {
		return ""
	}

	segment = strings.ReplaceAll(segment, " ", "-")
	invalidChars := regexp.MustCompile(`[^a-z0-9._-]+`)
	segment = invalidChars.ReplaceAllString(segment, "-")
	segment = strings.Trim(segment, "-._")

	return segment
}

func formatProviderName(provider string) string {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		return "Unknown"
	}

	overrides := map[string]string{
		"aws":                "AWS",
		"ec-council":         "EC-Council",
		"eccouncil":          "EC-Council",
		"isc2":               "ISC2",
		"isaca":              "ISACA",
		"paloalto-networks":  "Palo Alto Networks",
		"palo-alto-networks": "Palo Alto Networks",
		"servicenow":         "ServiceNow",
		"vmware":             "VMware",
		"lpi":                "LPI",
	}
	if label, ok := overrides[provider]; ok {
		return label
	}

	parts := strings.Fields(strings.ReplaceAll(provider, "-", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
