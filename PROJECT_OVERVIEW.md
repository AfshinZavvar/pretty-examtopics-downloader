# Project Overview

This document is a code-verified guide to the current repository. It complements
the user-oriented README and the visual `architecture.svg` diagram.

## Purpose

ExamTopics Downloader is an interactive Go command-line application. It discovers
certification providers and exams from ExamTopics, scrapes matching question
discussion pages, supplements them with the freely visible `Reveal Solution`
payload when available, and writes a browser-based exam simulator as one HTML file.
It reports the question count advertised on the public exam landing page separately
from the number of accessible discussion-backed question pages.

The tool does not expose a server or API. Its only normal input is CLI flags and
interactive terminal selections. Complete extraction writes
`{provider}_{exam}.html`; recovered output from incomplete discovery or extraction
is named `{provider}_{exam}.partial.html` in the current working directory.

## End-to-end flow

1. `cmd/main.go` parses four flags:
   - `-debug` enables internal diagnostic logging.
   - `-no-cache` bypasses both provider-index and question-page caches.
   - `-refresh-index` forces a fresh provider index while retaining question pages.
   - `-recent-comments-days N` filters by recent dated comment activity; an
     explicitly supplied `0` disables filtering, while an omitted flag triggers a
     prompt.
2. Provider discovery concurrently fetches and merges unique, sorted providers from:
   - `https://www.examtopics.com/exams/`
   - `https://www.examtopics.com/discussions/`
3. The official exam list and cached discussion-derived slugs are shown immediately.
   `/scan` starts bounded discussion discovery, `/all` selects every indexed
   public discussion,
   and `/reload` reloads only the official list. Typing ordinary text filters the
   current menu, while `/clear` restores the full list. The older `/refresh`,
   `/filter TEXT`, `/f TEXT`, and `/` forms remain compatible but are not shown in
   the simplified interactive guide.
4. A discussion scan uses eight workers, a two-minute deadline, real progress, and
   periodic partial-cache checkpoints. Interrupted scans resume missing pages.
5. `fetch.GetAllPages` reuses the provider index, filters its links locally for the
   selected exam, deduplicates them, and sorts them by topic/question number.
6. `GetOfficialExamQuestionCount` reads the count advertised on the public exam
   landing page. This is metadata, not a promise that every body is accessible.
7. `FetchViewSolutions` makes one best-effort request to
   `/exams/{provider}/{exam}/view/` and indexes visible site-provided solutions by the
   question body's `data-id`.
8. Question pages are fetched through a fixed 15-worker pool. Cache misses are
   adaptively paced; cache hits return without consuming the request budget.
9. The optional recent-comment filter is applied after extraction. A question with
   no parseable comment date is retained.
10. `utils.WriteDataWithSelection` renders question cards into the HTML template,
   replaces the template's sample cards, derives provider/exam header metadata, and
   writes the final HTML file.

## Answer preference order

The project distinguishes site-provided answers from weaker fallbacks. None is
independently verified against the certification provider:

1. An ExamTopics viewer solution from `/exams/{provider}/{exam}/view/` is
   preferred. For ordinary multiple choice, its letter drives the card's
   `data-correct` value. For image-answer questions, the viewer image,
   description, inline images, and reference URLs are rendered in a green
   `Correct Answer` block.
2. If no viewer solution is indexed, the parser uses `.correct-answer` text on
   the discussion page.
3. If that text is empty, options marked `.correct-hidden` provide the answer
   letters.
4. If neither page answer marker exists, the most-voted community answer is selected
   from the hidden `.voted-answers-tally` JSON.
5. For HOTSPOT/drag-and-drop pages without a viewer solution, the question's own
   answer-area image is shown in an orange `Answer Area (unverified)` block. It is
   not represented as a verified correct answer.

When no letter can be determined, the renderer leaves `data-correct` empty rather
than guessing option A.

## Repository map

| Path | Responsibility |
| --- | --- |
| `cmd/main.go` | CLI flags, prompts, filtering menus, status output, output naming, and top-level error handling. |
| `internal/constants/constants.go` | HTTP timeouts, concurrency, retry, backoff, and adaptive-rate defaults. |
| `internal/fetch/fetch.go` | HTTP GET/retry layer, provider and exam discovery, slug normalization, discussion URL filtering, and page-list parsing. |
| `internal/fetch/scraper.go` | Concurrent extraction orchestration and per-question parsing of text, options, exhibits, answers, votes, and comments. |
| `internal/fetch/exam_metadata.go` | Advertised question-count retrieval and parsing from public exam landing pages. |
| `internal/fetch/view_solutions.go` | ExamTopics viewer `Reveal Solution` extraction and answer-description serialization. |
| `internal/fetch/page_cache.go` | SHA-256-keyed raw question-page cache with a 24-hour TTL. |
| `internal/fetch/provider_index.go` | Bounded parallel provider indexing, progress reporting, checkpoints, and resume. |
| `internal/fetch/exam_cache.go` | Versioned provider link/slug/page-progress cache and legacy slug-cache migration. |
| `internal/fetch/debug.go` | Package-level debug logging switch. |
| `internal/models/question.go` | In-memory question, comment, and viewer-solution structures. |
| `internal/models/transport.go` | Tuned reusable HTTP transport. |
| `internal/utils/utils.go` | Text cleanup, link sorting/deduplication, timing, HTTP client construction, adaptive limiter, and recent-comment filtering. |
| `internal/utils/files.go` | Template loading, metadata derivation, safe server-side card rendering, answer parsing, image handling, comments serialization, and output writing. |
| `internal/templates/template.html` | Embedded HTML/CSS/JavaScript exam simulator shell. It contains three sample cards that are replaced during generation. |
| `internal/templates/embed.go` | Embeds `template.html` into the executable for standalone distribution. |
| `build.bat` | Windows amd64/arm64 build, optional UPX compression, and output under `dist/`. |
| `architecture.svg` | High-level visual data-flow diagram. |
| `*_test.go` | Parser, renderer, retry, limiter, discovery, filtering, and embedding unit tests. |

## Data model

`models.QuestionData` is the pipeline boundary between scraping and rendering. It
contains the question metadata and text, question-side and answer-side exhibit
URLs, raw option strings, fallback answer text, source link and stable question ID,
structured comments, and an optional viewer-derived `AnswerSolution`.

`AnswerSolution` separates a letter answer from answer images, prose description,
inline description images, and reference URLs. Inline description images are
temporarily represented as `[[IMG:<url>]]` markers so their document order can be
preserved through the plain-text model and restored by the renderer.

## Network and reliability behavior

- Question requests use a 20-second timeout and three retries; official/index
  metadata uses a 10-second timeout and one retry.
- Requests use browser-like headers and a fixed ExamTopics referer.
- HTTP 429, 502, 503, and 504 are retryable; other non-200 responses are reported
  and returned as failures.
- Exponential backoff starts at one second, doubles per retry, and adds up to
  499 ms of jitter. `Retry-After` and backoff are combined into one wait, capped at
  60 seconds for server-provided delays.
- Provider indexing uses eight workers; question extraction uses 15 workers.
- The adaptive request pacer starts at 2 requests/second, may rise by 0.5 after
  every eight successes to a maximum of 8, and halves on a retryable throttling
  response to a minimum of 0.5.
- The final console summary reports how many discovered question pages were
  successfully extracted, cache hits, retries, and failed-link previews.

## Cache behavior

Two independent disk caches live under the OS user cache directory, falling back
to the current directory when that location cannot be resolved:

| Cache | Content | Key/file | TTL | Disabled by `-no-cache` |
| --- | --- | --- | --- | --- |
| Question-page cache | Raw discussion-page HTML | SHA-256 of URL under `pages/` | 24 hours | Yes |
| Provider index cache | Links, slugs, completed pages, and completion state | `provider_discussion_index_v2.json` | 24 hours | Yes |

Cached question pages skip adaptive pacing. Complete provider indexes return
immediately; partial indexes resume missing pages. The legacy slug-only cache is
imported as menu data and marked incomplete. Cache failures are best-effort and
visible only with debug logging.

## Generated HTML application

Question cards and application logic are stored in the generated HTML. The browser
UI supports:

- a light certification-workbook design with a remembered dark-mode toggle;
- an answered-question progress rail and responsive mobile layout;
- collapsible question cards;
- single- and multi-select answers inferred from the number of correct letters;
- submit, retry, sneak-peek, and complete restart flows;
- score tracking for submitted answers (peeked answers are not counted);
- full-text and `Q<number>` search with highlighting;
- question/answer/option image display and image zoom;
- comments in a modal; and
- links back to the original discussion after submission or reveal.

The file is not fully offline: it imports Manrope and IBM Plex Mono from Google Fonts and
keeps ExamTopics exhibit/answer images as remote URLs. No images are downloaded
into the repository or embedded as data URLs.

## Build and dependencies

- Module: `examtopics-downloader`
- Declared Go toolchain baseline: Go 1.24.2
- Direct dependencies:
  - `github.com/PuerkitoBio/goquery` for DOM querying and HTML parsing.
  - `golang.org/x/net/html` for ordered HTML-node traversal.
- `build.bat [amd64|arm64] [auto|off]` cross-compiles a stripped, CGO-disabled
  Windows executable and optionally compresses it with UPX when available.
- The template is embedded, so a built executable does not need the repository's
  template file beside it.

Recommended verification commands are:

```text
go test ./...
go vet ./...
go build ./cmd
```

The automated suite covers HTML parsing fallbacks, provider/exam normalization,
viewer answers, advertised-count parsing, image-heavy questions, rendering,
comment dates, menu commands, request retries, cache behavior, cancellation, and
adaptive rate changes. End-to-end tests against the live website and browser
automation tests are not part of the committed suite.

## Important constraints and maintenance notes

- Scraping depends on ExamTopics URLs, CSS classes, data attributes, and embedded
  JSON. Markup or anti-bot changes can break discovery or extraction.
- The tool has no authenticated-session or cookie support. Viewer-solution
  coverage is therefore limited to what ExamTopics exposes publicly.
- A two-minute provider scan can be partial. Partial extraction now returns an
  error alongside recovered questions, produces a `*.partial.html` file, and tells
  the user to resume the cached scan instead of presenting it as complete.
- `all-discussions` cannot fetch `/view/` solutions because it has no
  concrete exam slug.
- `/reload` intentionally refreshes only the official list; use `-refresh-index`
  followed by `/scan`, or proceed to extraction, to rebuild discussion listings.
- Generic/Oracle version normalization intentionally groups some versioned exam
  slugs. The console prints grouped variants after question-link discovery.
- If the initial page-count request fails, discovery returns an error instead of
  pretending the provider has only one page.
- Unknown-answer multiple-choice cards correctly avoid inventing answer A, but the
  current browser UI still allows submission and can show a blank correct-answer
  message.
- Comments are scraped from untrusted remote content and later interpolated into
  modal HTML by JavaScript. Option text is also restored from a `data-original`
  attribute using `innerHTML` during search. Generated files should be treated as
  untrusted content until these browser-side insertions use text nodes or explicit
  escaping.
- There is no CI workflow in this repository snapshot; only GitHub issue forms are
  configured.

## Legal and operational scope

The repository is MIT-licensed, but scraped ExamTopics content may have separate
copyright and terms-of-use restrictions. The README limits the intended scope to
educational, authorized use and notes that the software grants no redistribution
rights.
