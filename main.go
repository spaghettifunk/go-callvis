// go-callvis: a tool to help visualize the call graph of a Go program.
package main

import (
	"context"
	"flag"
	"fmt"
	"go/build"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ofabry/go-callvis/analysis"
	"github.com/ofabry/go-callvis/pkg/dot"
	"github.com/ofabry/go-callvis/pkg/logger"
	"github.com/pkg/browser"
	"golang.org/x/tools/go/buildutil"
)

const Usage = `go-callvis: visualize call graph of a Go program.

Usage:

  go-callvis [flags] package

  Package should be main package, otherwise -tests flag must be used.

Flags:
`

func parseHTTPAddr(addr string) string {
	host, port, _ := net.SplitHostPort(addr)
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "80"
	}
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%s", host, port),
	}
	return u.String()
}

func openBrowser(url string) {
	time.Sleep(time.Millisecond * 100)
	if err := browser.OpenURL(url); err != nil {
		log.Printf("OpenURL error: %v", err)
	}
}

func outputDot(analysis *analysis.Analysis, fname string, outputFormat string) {
	if e := analysis.ProcessListArgs(); e != nil {
		log.Fatalf("%v\n", e)
	}

	output, err := analysis.Render(analysis.Minlen, analysis.PrintOptions)
	if err != nil {
		log.Fatalf("%v\n", err)
	}

	log.Println("writing dot output..")

	writeErr := os.WriteFile(fmt.Sprintf("%s.gv", fname), output, 0755)
	if writeErr != nil {
		log.Fatalf("%v\n", writeErr)
	}

	log.Printf("converting dot to %s..\n", outputFormat)

	_, err = dot.DotToImage(*graphvizFlag, fname, outputFormat, output)
	if err != nil {
		log.Fatalf("%v\n", err)
	}
}

var (
	focusFlag    = flag.String("focus", "main", "Focus specific package using name or import path.")
	groupFlag    = flag.String("group", "pkg", "Grouping functions by packages and/or types [pkg, type] (separated by comma)")
	limitFlag    = flag.String("limit", "", "Limit package paths to given prefixes (separated by comma)")
	ignoreFlag   = flag.String("ignore", "", "Ignore package paths containing given prefixes (separated by comma)")
	includeFlag  = flag.String("include", "", "Include package paths with given prefixes (separated by comma)")
	nostdFlag    = flag.Bool("nostd", false, "Omit calls to/from packages in standard library.")
	nointerFlag  = flag.Bool("nointer", false, "Omit calls to unexported functions.")
	cacheDir     = flag.String("cacheDir", "", "Enable caching to avoid unnecessary re-rendering, you can force rendering by adding 'refresh=true' to the URL query or emptying the cache directory")
	graphvizFlag = flag.Bool("graphviz", false, "Use Graphviz's dot program to render images.")
	debugFlag    = flag.Bool("debug", true, "Enable verbose log.")
	outputFormat = flag.String("format", "svg", "output file format [svg | png | jpg | ...]")
)

var (
	minlen    uint
	nodesep   float64
	nodeshape string
	nodestyle string
	rankdir   string
)

var (
	version = "v0.8.0"
	commit  = "(unknown)"
)

func Version() string {
	return fmt.Sprintf("%s built from git %s", version, commit)
}

// noinspection GoUnhandledErrorResult
func main() {
	flag.Var((*buildutil.TagsFlag)(&build.Default.BuildTags), "tags", buildutil.TagsFlagDoc)
	// Graphviz options
	flag.UintVar(&minlen, "minlen", 2, "Minimum edge length (for wider output).")
	flag.Float64Var(&nodesep, "nodesep", 0.35, "Minimum space between two adjacent nodes in the same rank (for taller output).")
	flag.StringVar(&nodeshape, "nodeshape", "box", "graph node shape (see graphvis manpage for valid values)")
	flag.StringVar(&nodestyle, "nodestyle", "filled,rounded", "graph node style (see graphvis manpage for valid values)")
	flag.StringVar(&rankdir, "rankdir", "LR", "Direction of graph layout [LR | RL | TB | BT]")

	flag.Parse()

	testFlag := flag.Bool("tests", false, "Include test code.")
	httpFlag := flag.String("http", ":7878", "HTTP service address.")
	skipBrowser := flag.Bool("skipbrowser", false, "Skip opening browser.")
	outputFile := flag.String("file", "", "output filename - omit to use server mode")
	callgraphAlgo := flag.String("algo", "cha", fmt.Sprintf("The algorithm used to construct the call graph. Possible values inlcude: %q, %q, %q",
		analysis.CallGraphTypeStatic, analysis.CallGraphTypeCha, analysis.CallGraphTypeRta))

	versionFlag := flag.Bool("version", false, "Show version and exit.")

	if *versionFlag {
		fmt.Fprintln(os.Stderr, Version())
		os.Exit(0)
	}
	if *debugFlag {
		log.SetFlags(log.Lmicroseconds)
	}

	if flag.NArg() != 1 {
		fmt.Fprint(os.Stderr, Usage)
		flag.PrintDefaults()
		os.Exit(2)
	}

	l := 0
	if *debugFlag {
		l = -4
	}
	logger.InitializeLogger(logger.LogLevel(l))

	args := flag.Args()
	tests := *testFlag
	httpAddr := *httpFlag
	urlAddr := parseHTTPAddr(httpAddr)

	a := analysis.NewAnalysis(*outputFile)
	a.OptsSetup(*cacheDir, *focusFlag, *groupFlag, *ignoreFlag, *includeFlag, *limitFlag, *nointerFlag, false, *nostdFlag, analysis.CallGraphType(*callgraphAlgo))

	a.Minlen = minlen
	a.PrintOptions = map[string]string{
		"minlen":    fmt.Sprint(minlen),
		"nodesep":   fmt.Sprint(nodesep),
		"nodeshape": fmt.Sprint(nodeshape),
		"nodestyle": fmt.Sprint(nodestyle),
		"rankdir":   fmt.Sprint(rankdir),
	}

	if err := a.DoAnalysis(analysis.CallGraphType(*callgraphAlgo), "", tests, args); err != nil {
		logger.LogFatal(err.Error())
	}

	hdl := http.HandlerFunc(handler)
	wrappedHandler := InjectAnalysisMiddleware(a)(hdl)

	http.Handle("/", wrappedHandler)

	if *outputFile == "" {
		*outputFile = "output"
		if !*skipBrowser {
			go openBrowser(urlAddr)
		}

		log.Printf("http serving at %s", urlAddr)

		if err := http.ListenAndServe(httpAddr, nil); err != nil {
			logger.LogFatal(err.Error())
		}
	} else {
		outputDot(a, *outputFile, *outputFormat)
	}
}

// Key type to avoid context key collisions
type contextKey string

const analysisKey contextKey = "analysis"

// Middleware to inject MyObject
func InjectAnalysisMiddleware(obj *analysis.Analysis) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add the object to the context
			ctx := context.WithValue(r.Context(), analysisKey, obj)
			// Pass the request with the new context to the next handler
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Helper to retrieve analysis.Analysis from the context
func GetAnalysisFromContext(ctx context.Context) (*analysis.Analysis, bool) {
	obj, ok := ctx.Value(analysisKey).(*analysis.Analysis)
	return obj, ok
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && !strings.HasSuffix(r.URL.Path, ".svg") {
		http.NotFound(w, r)
		return
	}

	logger.LogDebug("----------------------")
	logger.LogDebug(" => handling request:  %v", r.URL)
	logger.LogDebug("----------------------")

	analysis, ok := GetAnalysisFromContext(r.Context())
	if !ok {
		http.Error(w, "Object not found in context", http.StatusInternalServerError)
		return
	}

	// .. and allow overriding by HTTP params
	analysis.OverrideByHTTP(r)

	var img string
	if img = analysis.FindCachedImg(); img != "" {
		log.Println("serving file:", img)
		http.ServeFile(w, r, img)
		return
	}

	// Convert list-style args to []string
	if e := analysis.ProcessListArgs(); e != nil {
		http.Error(w, "invalid parameters", http.StatusBadRequest)
		return
	}

	output, err := analysis.Render(analysis.Minlen, analysis.PrintOptions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Form.Get("format") == "dot" {
		log.Println("writing dot output..")
		fmt.Fprint(w, string(output))
		return
	}

	log.Printf("converting dot to %s..\n", *outputFormat)

	img, err = dot.DotToImage(*graphvizFlag, "", *outputFormat, output)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = analysis.CacheImg(img)
	if err != nil {
		http.Error(w, "cache img error: "+err.Error(), http.StatusBadRequest)
		return
	}

	log.Println("serving file:", img)
	http.ServeFile(w, r, img)
}
