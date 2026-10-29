// Package main génère docs/devportal.html — SPA statique KubeManager.
//
// Le portail embarque un manifeste léger inline puis charge la documentation
// à la demande via des chunks JS locaux (docs/devportal-data/**).
//
// Sources markdown :
//   - internal/modules/<name>/docs/<name>_technique.md
//   - internal/modules/<name>/docs/AI_CONTEXT.md
//   - docs/interne/<slug>.md                     (guides internes)
//   - docs/kubemanager_technique.md              (vue d'ensemble technique)
//   - docs/AI_CONTEXT.md                         (vue d'ensemble AI)
//
// Usage :
//
//	go run ./cmd/dev-docs
//	go run ./cmd/dev-docs --version v0.2.0 --output docs/devportal.html
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed template.html
var htmlTemplate string

var moduleIcons = map[string]string{
	"auth":      "lock",
	"identity":  "fingerprint",
	"projects":  "package",
	"workloads": "layers",
	"clusters":  "globe",
	"realtime":  "zap",
}

var moduleLabels = map[string]string{
	"auth":      "Auth",
	"identity":  "Identity",
	"projects":  "Projects",
	"workloads": "Workloads",
	"clusters":  "Clusters",
	"realtime":  "Realtime",
}

var defaultStatuses = map[string]string{
	"auth":      "stable",
	"identity":  "stable",
	"projects":  "stable",
	"workloads": "stable",
	"clusters":  "stable",
	"realtime":  "stable",
}

var guideIcons = map[string]string{
	"interne": "book-open",
}

// skipModuleDirs : ne pas traiter ces sous-dossiers de internal/modules comme des modules.
var skipModuleDirs = map[string]bool{
	"utils": true,
}

type DocAsset struct {
	Content    *string
	UpdatedAt  time.Time
	SearchText string
}

type ModuleDoc struct {
	ID          string
	Label       string
	Icon        string
	Status      string
	Technique   DocAsset
	AI          DocAsset
	Warnings    []string
	LastUpdated time.Time
}

type GuidePageDoc struct {
	ID         string
	Label      string
	Content    string
	SearchText string
	UpdatedAt  time.Time
}

type GuideSectionDoc struct {
	ID    string
	Label string
	Icon  string
	Pages []GuidePageDoc
}

type GlobalDoc struct {
	Technique   DocAsset
	AI          DocAsset
	LastUpdated time.Time
}

// TabsAvailability : kubemanager n'a que 2 onglets (Dev + AI), pas de frontend.
type TabsAvailability struct {
	Technique bool `json:"technique"`
	AI        bool `json:"ai"`
}

type ModuleIndexJSON struct {
	ID              string           `json:"id"`
	Label           string           `json:"label"`
	Icon            string           `json:"icon"`
	Status          string           `json:"status"`
	Tabs            TabsAvailability `json:"tabs"`
	ChunkKey        string           `json:"chunkKey"`
	ChunkPath       string           `json:"chunkPath"`
	CoverageCount   int              `json:"coverageCount"`
	CoverageTotal   int              `json:"coverageTotal"`
	CoveragePercent int              `json:"coveragePercent"`
	LastUpdated     string           `json:"lastUpdated,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
}

type GuidePageIndexJSON struct {
	ID          string           `json:"id"`
	Label       string           `json:"label"`
	ChunkKey    string           `json:"chunkKey"`
	ChunkPath   string           `json:"chunkPath"`
	Tabs        TabsAvailability `json:"tabs"`
	LastUpdated string           `json:"lastUpdated,omitempty"`
}

type GuideSectionIndexJSON struct {
	ID    string               `json:"id"`
	Label string               `json:"label"`
	Icon  string               `json:"icon"`
	Pages []GuidePageIndexJSON `json:"pages"`
}

type GlobalIndexJSON struct {
	Label       string           `json:"label"`
	Icon        string           `json:"icon"`
	Tabs        TabsAvailability `json:"tabs"`
	ChunkKey    string           `json:"chunkKey"`
	ChunkPath   string           `json:"chunkPath"`
	LastUpdated string           `json:"lastUpdated,omitempty"`
}

type SearchIndexRefJSON struct {
	ChunkKey    string `json:"chunkKey"`
	ChunkPath   string `json:"chunkPath"`
	RecordCount int    `json:"recordCount"`
}

type PortalIndexJSON struct {
	Version      string                  `json:"version"`
	GeneratedAt  string                  `json:"generatedAt"`
	TotalModules int                     `json:"totalModules"`
	WarnCount    int                     `json:"warnCount"`
	GuideCount   int                     `json:"guideCount"`
	Modules      []ModuleIndexJSON       `json:"modules"`
	Guides       []GuideSectionIndexJSON `json:"guides"`
	Global       *GlobalIndexJSON        `json:"global,omitempty"`
	Search       *SearchIndexRefJSON     `json:"search,omitempty"`
}

type ChunkTabsJSON struct {
	Technique *string `json:"technique,omitempty"`
	AI        *string `json:"ai,omitempty"`
}

type ChunkJSON struct {
	Tabs ChunkTabsJSON `json:"tabs"`
}

type SearchRecordJSON struct {
	ID   string        `json:"id"`
	Tabs ChunkTabsJSON `json:"tabs"`
}

type SearchChunkJSON struct {
	Records []SearchRecordJSON `json:"records"`
}

type PortalTplData struct {
	DocsDataJSON template.JS
}

func markdownToSearchText(value string) string {
	replacer := strings.NewReplacer(
		"```", " ",
		"`", " ",
		"#", " ",
		"*", " ",
		"_", " ",
		"[", " ",
		"]", " ",
		"(", " ",
		")", " ",
		"|", " ",
		">", " ",
		"-", " ",
		"+", " ",
		"\r", " ",
		"\n", " ",
		"\t", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(value)), " ")
}

func readDocAsset(path string) DocAsset {
	data, err := os.ReadFile(path)
	if err != nil {
		return DocAsset{}
	}
	content := string(data)
	asset := DocAsset{Content: &content, SearchText: markdownToSearchText(content)}
	if info, statErr := os.Stat(path); statErr == nil {
		asset.UpdatedAt = info.ModTime().UTC()
	}
	return asset
}

func readFirstDocAsset(paths ...string) DocAsset {
	for _, path := range paths {
		asset := readDocAsset(path)
		if assetAvailable(asset) {
			return asset
		}
	}
	return DocAsset{}
}

func assetAvailable(asset DocAsset) bool {
	return asset.Content != nil && strings.TrimSpace(*asset.Content) != ""
}

func optionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func maxTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func formatUpdatedDate(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02")
}

func extractTitle(data string, filename string) string {
	for _, line := range strings.SplitN(data, "\n", 20) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	name := strings.TrimSuffix(filename, ".md")
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	words := strings.Fields(name)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func slugify(s string) string {
	s = strings.TrimSuffix(s, ".md")
	s = strings.ToLower(strings.NewReplacer(" ", "-", "_", "-").Replace(s))
	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for _, r := range s {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func loadStatuses(docsDir string) map[string]string {
	statuses := make(map[string]string)
	for k, v := range defaultStatuses {
		statuses[k] = v
	}
	data, err := os.ReadFile(filepath.Join(docsDir, "module-status.json"))
	if err != nil {
		return statuses
	}
	var overrides map[string]string
	if err := json.Unmarshal(data, &overrides); err == nil {
		for k, v := range overrides {
			statuses[k] = v
		}
	}
	return statuses
}

func tabsAvailability(technique, ai *string) TabsAvailability {
	return TabsAvailability{
		Technique: technique != nil && strings.TrimSpace(*technique) != "",
		AI:        ai != nil && strings.TrimSpace(*ai) != "",
	}
}

func outputDataDir(outputPath string) string {
	base := strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))
	return filepath.Join(filepath.Dir(outputPath), base+"-data")
}

func relativeFromOutput(outputPath, target string) string {
	rel, err := filepath.Rel(filepath.Dir(outputPath), target)
	if err != nil {
		return filepath.ToSlash(filepath.Base(target))
	}
	return filepath.ToSlash(rel)
}

func prepareOutputDirs(outputPath, dataDir string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dataDir); err != nil {
		return err
	}
	return os.MkdirAll(dataDir, 0o755)
}

func writeChunkFile(path, chunkKey string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("window.__DEVPORTAL_REGISTER_CHUNK(%q, %s);\n", chunkKey, body)
	return os.WriteFile(path, []byte(script), 0o644)
}

func writeGlobalChunk(global *GlobalDoc, outputPath, dataDir string) (*GlobalIndexJSON, int, error) {
	if global == nil {
		return nil, 0, nil
	}
	chunkKey := "global"
	chunkFile := filepath.Join(dataDir, "global.js")
	payload := ChunkJSON{Tabs: ChunkTabsJSON{Technique: global.Technique.Content, AI: global.AI.Content}}
	if err := writeChunkFile(chunkFile, chunkKey, payload); err != nil {
		return nil, 0, err
	}
	return &GlobalIndexJSON{
		Label:       "Vue d'ensemble",
		Icon:        "layers",
		Tabs:        tabsAvailability(global.Technique.Content, global.AI.Content),
		ChunkKey:    chunkKey,
		ChunkPath:   relativeFromOutput(outputPath, chunkFile),
		LastUpdated: formatUpdatedDate(global.LastUpdated),
	}, 1, nil
}

func moduleCoverage(mod ModuleDoc) (int, int, int) {
	count := 0
	if assetAvailable(mod.Technique) {
		count++
	}
	if assetAvailable(mod.AI) {
		count++
	}
	total := 2
	return count, total, count * 100 / total
}

func writeModuleChunks(modules []ModuleDoc, outputPath, dataDir string) ([]ModuleIndexJSON, int, error) {
	index := make([]ModuleIndexJSON, 0, len(modules))
	chunkCount := 0
	for _, mod := range modules {
		chunkKey := "module:" + mod.ID
		chunkFile := filepath.Join(dataDir, "modules", slugify(mod.ID)+".js")
		payload := ChunkJSON{Tabs: ChunkTabsJSON{Technique: mod.Technique.Content, AI: mod.AI.Content}}
		if err := writeChunkFile(chunkFile, chunkKey, payload); err != nil {
			return nil, 0, err
		}
		coverageCount, coverageTotal, coveragePercent := moduleCoverage(mod)
		index = append(index, ModuleIndexJSON{
			ID:              mod.ID,
			Label:           mod.Label,
			Icon:            mod.Icon,
			Status:          mod.Status,
			Tabs:            tabsAvailability(mod.Technique.Content, mod.AI.Content),
			ChunkKey:        chunkKey,
			ChunkPath:       relativeFromOutput(outputPath, chunkFile),
			CoverageCount:   coverageCount,
			CoverageTotal:   coverageTotal,
			CoveragePercent: coveragePercent,
			LastUpdated:     formatUpdatedDate(mod.LastUpdated),
			Warnings:        mod.Warnings,
		})
		chunkCount++
	}
	return index, chunkCount, nil
}

func writeGuideChunks(sections []GuideSectionDoc, outputPath, dataDir string) ([]GuideSectionIndexJSON, int, error) {
	index := make([]GuideSectionIndexJSON, 0, len(sections))
	chunkCount := 0
	for _, section := range sections {
		pages := make([]GuidePageIndexJSON, 0, len(section.Pages))
		for _, page := range section.Pages {
			content := page.Content
			chunkKey := "guide:" + page.ID
			chunkFile := filepath.Join(dataDir, "guides", slugify(section.ID), slugify(page.ID)+".js")
			payload := ChunkJSON{Tabs: ChunkTabsJSON{Technique: &content}}
			if err := writeChunkFile(chunkFile, chunkKey, payload); err != nil {
				return nil, 0, err
			}
			pages = append(pages, GuidePageIndexJSON{
				ID:          page.ID,
				Label:       page.Label,
				ChunkKey:    chunkKey,
				ChunkPath:   relativeFromOutput(outputPath, chunkFile),
				Tabs:        TabsAvailability{Technique: true},
				LastUpdated: formatUpdatedDate(page.UpdatedAt),
			})
			chunkCount++
		}
		index = append(index, GuideSectionIndexJSON{ID: section.ID, Label: section.Label, Icon: section.Icon, Pages: pages})
	}
	return index, chunkCount, nil
}

func writeSearchIndex(global *GlobalDoc, modules []ModuleDoc, guides []GuideSectionDoc, outputPath, dataDir string) (*SearchIndexRefJSON, int, error) {
	records := make([]SearchRecordJSON, 0, len(modules)+len(guides)+1)
	if global != nil {
		records = append(records, SearchRecordJSON{
			ID: "__global",
			Tabs: ChunkTabsJSON{
				Technique: optionalString(global.Technique.SearchText),
				AI:        optionalString(global.AI.SearchText),
			},
		})
	}
	for _, mod := range modules {
		records = append(records, SearchRecordJSON{
			ID: mod.ID,
			Tabs: ChunkTabsJSON{
				Technique: optionalString(mod.Technique.SearchText),
				AI:        optionalString(mod.AI.SearchText),
			},
		})
	}
	for _, section := range guides {
		for _, page := range section.Pages {
			records = append(records, SearchRecordJSON{
				ID: page.ID,
				Tabs: ChunkTabsJSON{
					Technique: optionalString(page.SearchText),
				},
			})
		}
	}
	if len(records) == 0 {
		return nil, 0, nil
	}
	chunkKey := "search:index"
	chunkFile := filepath.Join(dataDir, "search", "index.js")
	payload := SearchChunkJSON{Records: records}
	if err := writeChunkFile(chunkFile, chunkKey, payload); err != nil {
		return nil, 0, err
	}
	return &SearchIndexRefJSON{
		ChunkKey:    chunkKey,
		ChunkPath:   relativeFromOutput(outputPath, chunkFile),
		RecordCount: len(records),
	}, 1, nil
}

func scanModules(modulesDir string, statuses map[string]string) ([]ModuleDoc, int, error) {
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read modules dir %q: %w", modulesDir, err)
	}

	var modules []ModuleDoc
	warnCount := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if skipModuleDirs[name] {
			continue
		}
		docsDir := filepath.Join(modulesDir, name, "docs")
		if _, err := os.Stat(docsDir); os.IsNotExist(err) {
			continue
		}

		icon := moduleIcons[name]
		if icon == "" {
			icon = "box"
		}
		label := moduleLabels[name]
		if label == "" {
			label = strings.ToUpper(name[:1]) + name[1:]
		}
		status := statuses[name]
		if status == "" {
			status = "stable"
		}

		mod := ModuleDoc{
			ID:     name,
			Label:  label,
			Icon:   icon,
			Status: status,
		}

		mod.Technique = readFirstDocAsset(
			filepath.Join(docsDir, name+"_technique.md"),
			filepath.Join(docsDir, "technical.md"),
		)
		mod.AI = readDocAsset(filepath.Join(docsDir, "AI_CONTEXT.md"))
		mod.LastUpdated = maxTime(mod.Technique.UpdatedAt, mod.AI.UpdatedAt)

		if !assetAvailable(mod.Technique) {
			mod.Warnings = append(mod.Warnings, "technique manquante")
		}
		if !assetAvailable(mod.AI) {
			mod.Warnings = append(mod.Warnings, "AI_CONTEXT manquant")
		}
		if len(mod.Warnings) > 0 {
			warnCount++
			for _, warning := range mod.Warnings {
				fmt.Printf("  ⚠️  [%s] %s\n", name, warning)
			}
		}

		modules = append(modules, mod)
	}

	sort.Slice(modules, func(i, j int) bool { return modules[i].ID < modules[j].ID })
	return modules, warnCount, nil
}

func scanGlobal(docsDir string) *GlobalDoc {
	global := &GlobalDoc{
		Technique: readDocAsset(filepath.Join(docsDir, "kubemanager_technique.md")),
		AI:        readDocAsset(filepath.Join(docsDir, "AI_CONTEXT.md")),
	}
	global.LastUpdated = maxTime(global.Technique.UpdatedAt, global.AI.UpdatedAt)
	if !assetAvailable(global.Technique) && !assetAvailable(global.AI) {
		return nil
	}
	return global
}

func scanGuideDir(dir, sectionID, sectionLabel string) (GuideSectionDoc, error) {
	icon := guideIcons[sectionID]
	if icon == "" {
		icon = "book-open"
	}
	section := GuideSectionDoc{ID: sectionID, Label: sectionLabel, Icon: icon}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return section, nil
		}
		return section, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		content := string(data)
		page := GuidePageDoc{
			ID:         sectionID + "-" + slugify(entry.Name()),
			Label:      extractTitle(content, entry.Name()),
			Content:    content,
			SearchText: markdownToSearchText(content),
		}
		if info, statErr := os.Stat(filePath); statErr == nil {
			page.UpdatedAt = info.ModTime().UTC()
		}
		section.Pages = append(section.Pages, page)
		fmt.Printf("   📄 [%s] %s\n", sectionID, entry.Name())
	}

	sort.Slice(section.Pages, func(i, j int) bool { return section.Pages[i].Label < section.Pages[j].Label })
	return section, nil
}

func main() {
	modulesDir := flag.String("modules-dir", "internal/modules", "Répertoire racine des modules")
	docsDir := flag.String("docs-dir", "docs", "Répertoire des docs généraux")
	outputPath := flag.String("output", "docs/devportal.html", "Fichier HTML de sortie")
	version := flag.String("version", "v0.1", "Version de la plateforme")
	flag.Parse()

	fmt.Println("☸️  KubeManager DevPortal — génération en cours...")

	statuses := loadStatuses(*docsDir)
	modules, warnCount, err := scanModules(*modulesDir, statuses)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	global := scanGlobal(*docsDir)

	guideSpecs := []struct{ subdir, id, label string }{
		{"interne", "interne", "Guides Internes"},
	}
	var guides []GuideSectionDoc
	guideCount := 0
	for _, spec := range guideSpecs {
		section, err := scanGuideDir(filepath.Join(*docsDir, spec.subdir), spec.id, spec.label)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		if len(section.Pages) == 0 {
			continue
		}
		guides = append(guides, section)
		guideCount += len(section.Pages)
	}

	dataDir := outputDataDir(*outputPath)
	if err := prepareOutputDirs(*outputPath, dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Prepare output dirs: %v\n", err)
		os.Exit(1)
	}

	globalIndex, globalChunks, err := writeGlobalChunk(global, *outputPath, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Write global chunk: %v\n", err)
		os.Exit(1)
	}

	moduleIndex, moduleChunks, err := writeModuleChunks(modules, *outputPath, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Write module chunks: %v\n", err)
		os.Exit(1)
	}

	guideIndex, guideChunks, err := writeGuideChunks(guides, *outputPath, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Write guide chunks: %v\n", err)
		os.Exit(1)
	}

	searchIndex, searchChunks, err := writeSearchIndex(global, modules, guides, *outputPath, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Write search index: %v\n", err)
		os.Exit(1)
	}

	portal := PortalIndexJSON{
		Version:      *version,
		GeneratedAt:  time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		TotalModules: len(moduleIndex),
		WarnCount:    warnCount,
		GuideCount:   guideCount,
		Modules:      moduleIndex,
		Guides:       guideIndex,
		Global:       globalIndex,
		Search:       searchIndex,
	}

	jsonBytes, err := json.Marshal(portal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ JSON marshal: %v\n", err)
		os.Exit(1)
	}

	tmpl, err := template.New("devportal").Parse(htmlTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Template parse: %v\n", err)
		os.Exit(1)
	}

	out, err := os.Create(*outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Create %s: %v\n", *outputPath, err)
		os.Exit(1)
	}
	defer out.Close()

	if err := tmpl.Execute(out, PortalTplData{DocsDataJSON: template.JS(jsonBytes)}); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Template execute: %v\n", err)
		os.Exit(1)
	}

	chunkCount := globalChunks + moduleChunks + guideChunks + searchChunks
	fmt.Printf("\n✅ %d modules · %d guides · %d chunks lazy-loadés", len(moduleIndex), guideCount, chunkCount)
	if warnCount > 0 {
		fmt.Printf(" · ⚠️  %d warnings", warnCount)
	}
	fmt.Printf("\n📄 %s\n", *outputPath)
}
