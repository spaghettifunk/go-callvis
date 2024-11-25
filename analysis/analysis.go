package analysis

import (
	"errors"
	"fmt"
	"go/build"
	"go/types"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ofabry/go-callvis/pkg/logger"
	"github.com/ofabry/go-callvis/pkg/output"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/callgraph/static"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type CallGraphType string

const (
	CallGraphTypeStatic CallGraphType = "static"
	CallGraphTypeCha    CallGraphType = "cha"
	CallGraphTypeRta    CallGraphType = "rta"
)

// ==[ type def/func: analysis   ]===============================================
type renderOpts struct {
	cacheDir string
	focus    string
	group    []string
	ignore   []string
	include  []string
	limit    []string
	nointer  bool
	refresh  bool
	nostd    bool
	algo     CallGraphType
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func mainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main packages")
	}
	return mains, nil
}

// ==[ type def/func: Analysis   ]===============================================
type Analysis struct {
	opts         *renderOpts
	prog         *ssa.Program
	pkgs         []*ssa.Package
	mainPkg      *ssa.Package
	callgraph    *callgraph.Graph
	outputFormat string
	Minlen       uint
	PrintOptions map[string]string
}

func NewAnalysis(outputFormat string) *Analysis {
	return &Analysis{
		outputFormat: outputFormat,
	}
}

func (a *Analysis) DoAnalysis(
	algo CallGraphType,
	dir string,
	tests bool,
	args []string,
) error {
	allPackages := packages.NeedName |
		packages.NeedFiles |
		packages.NeedCompiledGoFiles |
		packages.NeedImports |
		packages.NeedDeps |
		packages.NeedExportFile |
		packages.NeedTypes |
		packages.NeedSyntax |
		packages.NeedTypesInfo |
		packages.NeedTypesSizes |
		packages.NeedModule | packages.NeedEmbedFiles | packages.NeedEmbedPatterns

	cfg := &packages.Config{
		Mode:       packages.LoadMode(allPackages),
		Tests:      tests,
		Dir:        dir,
		BuildFlags: getBuildFlags(),
	}

	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}

	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)
	prog.Build()

	var graph *callgraph.Graph
	var mainPkg *ssa.Package

	switch algo {
	case CallGraphTypeStatic:
		graph = static.CallGraph(prog)
	case CallGraphTypeCha:
		graph = cha.CallGraph(prog)
	case CallGraphTypeRta:
		mains, err := mainPackages(prog.AllPackages())
		if err != nil {
			return err
		}
		var roots []*ssa.Function
		mainPkg = mains[0]
		for _, main := range mains {
			roots = append(roots, main.Func("main"))
		}
		graph = rta.Analyze(roots, true).CallGraph
	default:
		return fmt.Errorf("invalid call graph type: %s", a.opts.algo)
	}

	//cg.DeleteSyntheticNodes()

	a.prog = prog
	a.pkgs = pkgs
	a.mainPkg = mainPkg
	a.callgraph = graph
	return nil
}

func (a *Analysis) OptsSetup(cacheDir string,
	focus string,
	group string,
	ignore string,
	include string,
	limit string,
	nointer bool,
	refresh bool,
	nostd bool,
	algo CallGraphType,
) {
	a.opts = &renderOpts{
		cacheDir: cacheDir,
		focus:    focus,
		group:    []string{group},
		ignore:   []string{ignore},
		include:  []string{include},
		limit:    []string{limit},
		nointer:  nointer,
		nostd:    nostd,
	}
}

func (a *Analysis) ProcessListArgs() (e error) {
	var groupBy []string
	var ignorePaths []string
	var includePaths []string
	var limitPaths []string

	for _, g := range strings.Split(a.opts.group[0], ",") {
		g := strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if g != "pkg" && g != "type" {
			e = errors.New("invalid group option")
			return
		}
		groupBy = append(groupBy, g)
		a.opts.group = groupBy
	}

	if len(a.opts.ignore) > 0 {
		for _, p := range strings.Split(a.opts.ignore[0], ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				ignorePaths = append(ignorePaths, p)
			}
		}
		a.opts.ignore = ignorePaths
	}

	if len(a.opts.include) > 0 {
		for _, p := range strings.Split(a.opts.include[0], ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				includePaths = append(includePaths, p)
			}
		}
		a.opts.include = includePaths
	}

	if len(a.opts.limit) > 0 {
		for _, p := range strings.Split(a.opts.limit[0], ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				limitPaths = append(limitPaths, p)
			}
		}
		a.opts.limit = limitPaths
	}

	return
}

func (a *Analysis) OverrideByHTTP(r *http.Request) {
	if f := r.FormValue("f"); f == "all" {
		a.opts.focus = ""
	} else if f != "" {
		a.opts.focus = f
	}
	if std := r.FormValue("std"); std != "" {
		a.opts.nostd = false
	}
	if inter := r.FormValue("nointer"); inter != "" {
		a.opts.nointer = true
	}
	if refresh := r.FormValue("refresh"); refresh != "" {
		a.opts.refresh = true
	}
	if g := r.FormValue("group"); g != "" {
		a.opts.group[0] = g
	}
	if l := r.FormValue("limit"); l != "" {
		a.opts.limit[0] = l
	}
	if ign := r.FormValue("ignore"); ign != "" {
		a.opts.ignore[0] = ign
	}
	if inc := r.FormValue("include"); inc != "" {
		a.opts.include[0] = inc
	}
}

// basically do printOutput() with previously checking
// focus option and respective package
func (a *Analysis) Render(minlen uint, options map[string]string) ([]byte, error) {
	var (
		err      error
		ssaPkg   *ssa.Package
		focusPkg *types.Package
	)

	if a.opts.focus != "" {
		if ssaPkg = a.prog.ImportedPackage(a.opts.focus); ssaPkg == nil {
			if strings.Contains(a.opts.focus, "/") {
				return nil, fmt.Errorf("focus failed: %v", err)
			}
			// try to find package by name
			var foundPaths []string
			for _, p := range a.pkgs {
				if p.Pkg.Name() == a.opts.focus {
					foundPaths = append(foundPaths, p.Pkg.Path())
				}
			}
			if len(foundPaths) == 0 {
				return nil, fmt.Errorf("focus failed, could not find package: %v", a.opts.focus)
			} else if len(foundPaths) > 1 {
				for _, p := range foundPaths {
					fmt.Fprintf(os.Stderr, " - %s\n", p)
				}
				return nil, fmt.Errorf("focus failed, found multiple packages with name: %v", a.opts.focus)
			}
			// found single package
			if ssaPkg = a.prog.ImportedPackage(foundPaths[0]); ssaPkg == nil {
				return nil, fmt.Errorf("focus failed: %v", err)
			}
		}
		focusPkg = ssaPkg.Pkg
		logger.LogDebug("focusing: %v", focusPkg.Path())
	}

	dot, err := output.PrintOutput(
		a.prog,
		a.mainPkg,
		a.callgraph,
		focusPkg,
		a.opts.limit,
		a.opts.ignore,
		a.opts.include,
		a.opts.group,
		a.opts.nostd,
		a.opts.nointer,
		minlen,
		options,
	)
	if err != nil {
		return nil, fmt.Errorf("processing failed: %v", err)
	}

	return dot, nil
}

func (a *Analysis) FindCachedImg() string {
	if a.opts.cacheDir == "" || a.opts.refresh {
		return ""
	}

	focus := a.opts.focus
	if focus == "" {
		focus = "all"
	}
	focusFilePath := focus + "." + a.outputFormat
	absFilePath := filepath.Join(a.opts.cacheDir, focusFilePath)

	if exists, err := pathExists(absFilePath); err != nil || !exists {
		log.Println("not cached img:", absFilePath)
		return ""
	}

	log.Println("hit cached img")
	return absFilePath
}

func (a *Analysis) CacheImg(img string) error {
	if a.opts.cacheDir == "" || img == "" {
		return nil
	}

	focus := a.opts.focus
	if focus == "" {
		focus = "all"
	}
	absCacheDirPrefix := filepath.Join(a.opts.cacheDir, focus)
	absCacheDirPath := strings.TrimRightFunc(absCacheDirPrefix, func(r rune) bool {
		return r != '\\' && r != '/'
	})
	err := os.MkdirAll(absCacheDirPath, os.ModePerm)
	if err != nil {
		return err
	}

	absFilePath := absCacheDirPrefix + "." + a.outputFormat
	_, err = copyFile(img, absFilePath)
	if err != nil {
		return err
	}

	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func copyFile(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)

	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()
	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

func getBuildFlags() []string {
	buildFlagTags := getBuildFlagTags(build.Default.BuildTags)
	if len(buildFlagTags) == 0 {
		return nil
	}

	return []string{buildFlagTags}
}

func getBuildFlagTags(buildTags []string) string {
	if len(buildTags) > 0 {
		return "-tags=" + strings.Join(buildTags, ",")
	}

	return ""
}
