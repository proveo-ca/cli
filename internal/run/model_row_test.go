// SPEC: _spec/internal/choiceui/wireframe.puml, _spec/defs/hermes/hermes-paradigm.puml
package run

import (
	"cmp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/proveo-ca/proveo/internal/agentsettings"
	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/credentials"
	"github.com/proveo-ca/proveo/internal/manifest"
	"github.com/proveo-ca/proveo/internal/sbx"
)

var (
	qwen       = preset("qwen3.8")
	inkling    = preset("inkling-2-mini")
	inklingMLX = preset("inkling-2-mini-mlx")
	bolt       = preset("nemotron-3.5-bolt")
	mimo       = preset("mimo-v2.6")
	lfm3       = preset("lfm3")
	minicpm    = preset("minicpm5")
)

func names(ms []localModel) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.name
	}
	return out
}

func preset(repo string) localModel {
	for _, m := range localModels {
		if m.repo == repo {
			return m
		}
	}
	panic("no preset " + repo)
}

// stubOllamaHost pins the platform and Ollama version the row reads; never call it from a parallel test.
func stubOllamaHost(t *testing.T, platform, version string) {
	t.Helper()
	origPlatform, origVersion := ollamaPlatform, hostOllamaVersion
	t.Cleanup(func() { ollamaPlatform, hostOllamaVersion = origPlatform, origVersion })
	ollamaPlatform = func() string { return platform }
	hostOllamaVersion = func() string { return version }
}

func hermesImagesMan() manifest.Manifest {
	return manifest.Manifest{Name: "hermes", Images: map[string]string{"hermes": "proveo/hermes:latest"}}
}

func selected(t *testing.T, localModel string) string {
	t.Helper()
	r, ok := modelRow(hermesImagesMan(), localModel, tierMini)
	if !ok {
		t.Fatal("no model row for hermes")
	}
	return r.Options[r.Selected]
}

func TestModelRowOrderRunsRiskierToSafer(t *testing.T) {
	t.Parallel()
	r, ok := modelRow(hermesImagesMan(), "", tierNano)
	if !ok {
		t.Fatal("no model row for hermes")
	}
	if want := []string{modelAPIKeys, bolt.label(), mimo.label()}; !slices.Equal(r.Options, want) {
		t.Errorf("options = %v, want %v", r.Options, want)
	}
	if r.Multi || r.Options[r.Selected] != modelAPIKeys {
		t.Errorf("want a single-select row defaulting to API keys, got multi=%v selected=%q", r.Multi, r.Options[r.Selected])
	}
}

func TestModelRowSwitchesPresetsAtTheHostCapacityCutoffs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		memory uint64
		local  []string
		cols   []int
	}{
		{0, nil, []int{0, 1}},
		{(6 << 30) - 1, nil, []int{0, 1}},
		{6 << 30, []string{"LFM3", "MiniCPM5"}, []int{0, 1, 2, 3}},
		{8 << 30, []string{"Bolt", "MiMo"}, []int{0, 1, 2, 3}},
		{(24 << 30) - 1, []string{"Bolt", "MiMo"}, []int{0, 1, 2, 3}},
		{24 << 30, []string{"Qwen 3.8"}, []int{0, 1, 3}},
		{(96 << 30) - 1, []string{"Qwen 3.8"}, []int{0, 1, 3}},
		{96 << 30, []string{"Nemotron", "Flash Next"}, []int{0, 1, 2, 3}},
		{128 << 30, []string{"Nemotron", "Flash Next"}, []int{0, 1, 2, 3}},
	} {
		keys := env(map[string]string{"OPENCODE_API_KEY": "zen"})
		man := opencodeMan()
		auth, hasAuth := authRow(man, keys, man.Name, "", "", "")
		r, ok := sourceRow(man, auth, hasAuth, "", modelTier(tc.memory))
		want := append([]string{modelAPIKeys, modelPlan}, tc.local...)
		if !ok || !slices.Equal(r.Options, want) {
			t.Errorf("%d bytes: options = %v (%v), want %v", tc.memory, r.Options, ok, want)
		}
		if !slices.Equal(r.Cols, tc.cols) {
			t.Errorf("%d bytes: cols = %v, want %v", tc.memory, r.Cols, tc.cols)
		}
	}
}

func TestAnExplicitModelCanCrossTheTierBoundary(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		tag, option string
		tier        int
	}{
		{"qwen3.8:latest", "Qwen 3.8", tierSmall},
		{"nemotron-3.5-super:120b-a12b-nvfp4", "Nemotron", tierMini},
		{"qwen3.8-flash-next:125b-a6b-q4_K_M", "Flash Next", 0},
	} {
		r, _ := modelRow(hermesImagesMan(), c.tag, c.tier)
		if got := r.Options[r.Selected]; got != c.option {
			t.Fatalf("tag %q selected %q, want %q", c.tag, got, c.option)
		}
		if n := len(r.Options) - 1; n > 2 {
			t.Errorf("tag %q at tier %d drew %d local models %v, want its column's slot, not a third", c.tag, c.tier, n, r.Options)
		}
		p := Params{LocalModel: c.tag}
		p.applySource(c.option, true)
		if p.LocalModel != c.tag || p.AuthVar != credentials.AuthLocal {
			t.Errorf("explicit tag became model %q and auth %q", p.LocalModel, p.AuthVar)
		}
	}
}

func TestLargePresetsArePinnedHuggingFaceImports(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{"nemotron-3.5-super", "qwen3.8-flash-next"} {
		m := preset(repo)
		imp, ok := sbx.ImportFor(m.tagFor("linux/amd64"))
		if !ok {
			t.Errorf("%s: %s has no registered import", m.name, m.tagFor("linux/amd64"))
			continue
		}
		if len(imp.Revision) != 40 || len(imp.Files) < 2 {
			t.Errorf("%s: import %s@%s with %d files, want a 40-hex revision and its shards", m.name, imp.Repo, imp.Revision, len(imp.Files))
		}
		for _, f := range imp.Files {
			if f.Size > 1<<20 && len(f.SHA256) != 64 {
				t.Errorf("%s: %s is %d bytes with no pinned sha256", m.name, f.Path, f.Size)
			}
		}
	}
	const custom = "nemotron-3.5-super:120b-a12b-nvfp4"
	if got := hostTagFor(custom); got != custom {
		t.Errorf("remembered custom Nemotron build = %q, want %q", got, custom)
	}
}

func TestModelRowRecognisesAManualLocalModel(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{"qwen3.8:latest", "qwen3.8:27b-mlx", "qwen3.8"} {
		if got := selected(t, tag); got != qwen.label() {
			t.Errorf("--local-model %s selected %q, want %q", tag, got, qwen.label())
		}
	}
	if got := selected(t, bolt.tag()); got != bolt.label() {
		t.Errorf("--local-model %s selected %q", bolt.tag(), got)
	}
	if got := modelLabelFor("qwen3.8:latest"); got != qwen.label() {
		t.Errorf("an untouched row must map back to the same option, so the flag's exact tag is kept; got %q", got)
	}
	r, _ := modelRow(hermesImagesMan(), "gemma4:12b", tierMini)
	if got := r.Options[r.Selected]; got != "gemma4:12b" || r.Help["gemma4:12b"] == "" {
		t.Errorf("an unlisted --local-model must appear as its own selected option with help, got %q in %v", got, r.Options)
	}
}

func TestModelRowOptionIsExactlyTheLocalModelFlag(t *testing.T) {
	t.Parallel()
	for opt, want := range map[string]string{
		modelAPIKeys: "",
		qwen.label(): qwen.tag(),
		bolt.label(): bolt.tag(),
		"gemma4:12b": "gemma4:12b",
	} {
		if got := localModelFor(opt); got != want {
			t.Errorf("%q → --local-model %q, want %q", opt, got, want)
		}
	}
}

func TestModelRowIsDrawnForEveryHarnessThatAcceptsLocalModel(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"hermes", "opencode", "codex", "claudecode"} {
		if _, ok := modelRow(manifest.Manifest{Name: name}, "", tierMini); !ok {
			t.Errorf("%s accepts --local-model but got no model row", name)
		}
	}
	cursor := manifest.Manifest{Name: "cursor"}
	if _, ok := modelRow(cursor, "", tierMini); ok {
		t.Error("cursor's inference is vendor-pinned; it must get no model row")
	}
	if got := rememberedLocalModel(cursor, "qwen3.8:latest"); got != "" {
		t.Errorf("cursor must not remember a --local-model, got %q", got)
	}
	if acceptsLocalModel("cursor") || !acceptsLocalModel("hermes") {
		t.Error("the row and the --local-model refusal must share one predicate")
	}
}

func TestLocalModelIsSeededFromTheCache(t *testing.T) {
	t.Parallel()
	none := func(string) string { return "" }
	var p Params
	p.seedFromCache(agentsettings.Choice{LocalModel: "qwen3.8:latest"}, none, false)
	if p.LocalModel != qwen.tag() {
		t.Errorf("a remembered qwen3.8:latest = %q, want this host's build %q", p.LocalModel, qwen.tag())
	}

	var custom Params
	custom.seedFromCache(agentsettings.Choice{LocalModel: "gemma4:12b"}, none, false)
	if custom.LocalModel != "gemma4:12b" {
		t.Errorf("an unlisted remembered tag must pass through, got %q", custom.LocalModel)
	}

	var legacy Params
	legacy.seedFromCache(agentsettings.Choice{ModelVariant: "hermes-qwen3.8"}, none, false)
	if want := qwen.tag(); legacy.LocalModel != want {
		t.Errorf("legacy modelVariant → %q, want %s", legacy.LocalModel, want)
	}

	flag := Params{LocalModel: "gemma4:12b"}
	flag.seedFromCache(agentsettings.Choice{LocalModel: "qwen3.8:latest"}, none, false)
	if flag.LocalModel != "gemma4:12b" {
		t.Errorf("--local-model must win over the cache, got %q", flag.LocalModel)
	}
}

func TestLocalModelFollowsTheHostPlatform(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{"darwin/arm64", "linux/amd64", "darwin/amd64", "linux/arm64"} {
		for _, c := range []struct {
			m   localModel
			tag string
		}{
			{qwen, "hf.co/huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF:Q4_K_M"},
			{inkling, "hf.co/AtomicChat/Inkling-2-Mini-Uncensored-GGUF:IQ3_S"},
			{bolt, "hf.co/mradermacher/NVIDIA-Nemotron-3.5-Bolt-E4B-BF16-heretic-3-i1-GGUF:i1-Q4_K_M"},
			{mimo, "hf.co/mradermacher/MiMo-V2.6-Distill-Qwen-9B-ARA-Abliterated-i1-GGUF:i1-IQ4_XS"},
			{lfm3, "hf.co/Vortecks/LFM3-1.7B-Heretic-Abliterated-GGUF:Q8_0"},
			{minicpm, "hf.co/Abiray/MiniCPM5-2B-heretic-abliterated-GGUF:Q6_K"},
		} {
			if got := c.m.tagFor(platform); got != c.tag {
				t.Errorf("%s tag on %s = %q, want %q", c.m.name, platform, got, c.tag)
			}
			if got := c.m.buildFor(platform); got != "GGUF" {
				t.Errorf("%s build on %s = %q, want GGUF", c.m.name, platform, got)
			}
		}
		wantMLX := map[bool]string{true: "proveo/inkling-2-mini-uncensored:26b-a5b-nvfp4", false: ""}[platform == "darwin/arm64"]
		if got := inklingMLX.tagFor(platform); got != wantMLX {
			t.Errorf("Inkling MLX import tag on %s = %q, want %q", platform, got, wantMLX)
		}
	}
}

func TestModelBuildLivesInTheHint(t *testing.T) {
	t.Parallel()
	r, ok := modelRow(hermesImagesMan(), "", tierNano)
	if !ok {
		t.Fatal("no model row")
	}
	for _, opt := range r.Options {
		if strings.Contains(opt, "GGUF") || strings.Contains(opt, "MLX") || strings.Contains(opt, "9B") || strings.Contains(opt, "E4B") {
			t.Errorf("build or size leaked into the option %q", opt)
		}
		if n := utf8.RuneCountInString(opt); n > 10 {
			t.Errorf("option %q is %d runes, want at most 10", opt, n)
		}
	}
	if cols := []int{0, 2, 3}; !slices.Equal(r.Cols, cols) {
		t.Errorf("cols = %v, want %v (plan's column stays empty)", r.Cols, cols)
	}
	h := r.Help[mimo.label()]
	if !strings.Contains(h, "MiMo-V2.6 Distill Qwen 9B abliterated (ARA)") || !strings.Contains(h, "GGUF") {
		t.Errorf("hint = %q, want the lead and the GGUF build", h)
	}
}

func sourceFor(t *testing.T, man manifest.Manifest, lookup map[string]string, localModel string) (choiceui.Row, bool) {
	t.Helper()
	auth, hasAuth := authRow(man, env(lookup), man.Name, "", "", "")
	return sourceRow(man, auth, hasAuth, localModel, tierMini)
}

func TestSourceRowMergesThePlanAndTheLocalModels(t *testing.T) {
	t.Parallel()
	keys := map[string]string{"OPENCODE_API_KEY": "zen", "ANTHROPIC_API_KEY": "sk"}
	r, ok := sourceFor(t, opencodeMan(), keys, "")
	if !ok {
		t.Fatal("opencode got no source row")
	}
	want := []string{modelAPIKeys, modelPlan, qwen.label()}
	if r.Label != rowModel || !slices.Equal(r.Options, want) {
		t.Errorf("row %q = %v, want %q = %v", r.Label, r.Options, rowModel, want)
	}
	if !slices.Equal(r.Cols, []int{0, 1, 3}) {
		t.Errorf("model cols = %v, want one option per column", r.Cols)
	}
	if len(r.Off) != len(r.Options) || r.Help[qwen.label()] == "" || r.Help[modelAPIKeys] == "" {
		t.Errorf("merged row lost its gates or help: off=%v help=%v", r.Off, r.Help)
	}
	if got := r.Options[r.Selected]; got != modelPlan {
		t.Errorf("with a plan available the row opens on %q, want %q", got, modelPlan)
	}

	r, _ = sourceFor(t, opencodeMan(), keys, "qwen3.8:latest")
	if got := r.Options[r.Selected]; got != qwen.label() {
		t.Errorf("--local-model preselects %q, want %q", got, qwen.label())
	}
}

func TestSourceRowKeepsCursorOffLocalModels(t *testing.T) {
	t.Parallel()
	r, ok := sourceFor(t, cursorMan(), map[string]string{"CURSOR_API_KEY": "c"}, "")
	if !ok {
		t.Fatal("cursor lost its plan row")
	}
	for _, m := range localModels {
		if slices.Contains(r.Options, m.label()) {
			t.Errorf("cursor-agent cannot target a custom endpoint, yet the row offers %q: %v", m.label(), r.Options)
		}
	}
}

func TestSourceRowWithoutAPlanIsTheModelRow(t *testing.T) {
	t.Parallel()
	r, ok := sourceFor(t, hermesImagesMan(), nil, "")
	if want := []string{modelAPIKeys, qwen.label()}; !ok || !slices.Equal(r.Options, want) {
		t.Errorf("hermes row = %v (%v), want %v", r.Options, ok, want)
	}
}

func TestApplySourceSetsLocalModelAndAuthTogether(t *testing.T) {
	t.Parallel()
	p := Params{}
	p.applySource(qwen.label(), true)
	if p.LocalModel != qwen.tag() || p.AuthVar != credentials.AuthLocal {
		t.Errorf("local on a plan harness: model=%q auth=%q", p.LocalModel, p.AuthVar)
	}
	p.applySource(modelPlan, true)
	if p.LocalModel != "" || p.AuthVar != credentials.AuthSubscription {
		t.Errorf("subscription must clear the local model: model=%q auth=%q", p.LocalModel, p.AuthVar)
	}
	p.applySource(modelAPIKeys, true)
	if p.LocalModel != "" || p.AuthVar != credentials.AuthUsage {
		t.Errorf("API keys on a plan harness = usage credits: model=%q auth=%q", p.LocalModel, p.AuthVar)
	}
	q := Params{}
	q.applySource(modelAPIKeys, false)
	if q.LocalModel != "" || q.AuthVar != "" {
		t.Errorf("API keys on hermes sets nothing: model=%q auth=%q", q.LocalModel, q.AuthVar)
	}
	keep := Params{LocalModel: "qwen3.8:latest"}
	keep.applySource(qwen.label(), false)
	if keep.LocalModel != "qwen3.8:latest" {
		t.Errorf("an untouched preselection must keep the flag's tag, got %q", keep.LocalModel)
	}
}

func TestApplyRemoteModelRoutesByHarness(t *testing.T) {
	t.Parallel()
	p := Params{Target: "opencode", Model: "opencode-go/glm-5.3"}
	if err := applyRemoteModel(&p); err != nil {
		t.Fatal(err)
	}
	if len(p.Extra) != 2 || p.Extra[0] != "-m" || p.Extra[1] != "opencode-go/glm-5.3" {
		t.Errorf("opencode extra = %v; want [-m opencode-go/glm-5.3]", p.Extra)
	}
	p = Params{Target: "codex", Model: "gpt-x"}
	if err := applyRemoteModel(&p); err != nil || p.Extra[0] != "--model" {
		t.Errorf("codex route = %v (%v); want --model", p.Extra, err)
	}
	p = Params{Target: "codex", Model: "gpt-5", Extra: []string{"resume", "--last", "--", "review this diff"}}
	want := []string{"resume", "--last", "--model", "gpt-5", "--", "review this diff"}
	if err := applyRemoteModel(&p); err != nil || !slices.Equal(p.Extra, want) {
		t.Errorf("codex resume route = %v (%v); want %v", p.Extra, err, want)
	}
	if err := applyRemoteModel(&Params{Target: "cursor", Model: "any/model"}); err == nil {
		t.Error("cursor must refuse --model (vendor-pinned)")
	}
	if err := applyRemoteModel(&Params{Target: "hermes"}); err != nil {
		t.Errorf("no --model set: %v", err)
	}
	p = Params{Target: "opencode"}
	if err := applyRemoteModel(&p); err != nil || len(p.Extra) != 0 {
		t.Errorf("no --model must touch nothing: %v (%v)", p.Extra, err)
	}
}

func TestModelPinKeysNamesTheProviderKey(t *testing.T) {
	t.Parallel()
	msgs := modelPinKeys(&Params{Target: "opencode", Model: "opencode-go/glm-5"}, nil, nil)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "needs OPENCODE_API_KEY (opencode), which is not set") {
		t.Errorf("pin keys = %v; want the OPENCODE_API_KEY ask", msgs)
	}
	if msgs := modelPinKeys(&Params{Target: "opencode", Model: "opencode-go/glm-5"}, []string{"opencode"}, nil); len(msgs) != 0 {
		t.Errorf("a detected provider must stay silent: %v", msgs)
	}
	if msgs := modelPinKeys(&Params{Target: "opencode"}, nil, nil); len(msgs) != 0 {
		t.Errorf("no pin must stay silent: %v", msgs)
	}
	if msgs := modelPinKeys(&Params{Target: "opencode", Model: "weird/unknown"}, nil, nil); len(msgs) != 0 {
		t.Errorf("an unknown provider must stay silent: %v", msgs)
	}
}

func TestMiniColumnTwoIsTheInklingImportOnAppleSilicon(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		platform, version string
		want              []string
	}{
		{"darwin/arm64", "0.40.17", []string{"Inkling", "Qwen 3.8"}},
		{"darwin/arm64", "0.40.17-rc4", []string{"Inkling", "Qwen 3.8"}},
		{"darwin/arm64", "v0.41.0", []string{"Inkling", "Qwen 3.8"}},
		{"darwin/arm64", "0.40.16", []string{"Qwen 3.8"}},
		{"darwin/arm64", "", []string{"Qwen 3.8"}},
		{"darwin/arm64", "garbage", []string{"Qwen 3.8"}},
		{"linux/amd64", "99.0.0", []string{"Qwen 3.8"}},
		{"darwin/amd64", "0.40.17", []string{"Qwen 3.8"}},
	} {
		if got := names(lineupFor(tierMini, c.platform, c.version)); !slices.Equal(got, c.want) {
			t.Errorf("Ollama %q on %s offers %v, want %v", c.version, c.platform, got, c.want)
		}
	}
	if got := lineupFor(tierMini, "darwin/arm64", "0.40.17"); got[0].repo != "inkling-2-mini-mlx" {
		t.Errorf("column 2 = %s, want the MLX import, not the GGUF Ollama cannot load", got[0].repo)
	}
}

func TestEveryRangeOffersItsPair(t *testing.T) {
	t.Parallel()
	for tier, want := range map[int][]string{
		tierSmall: {"Nemotron", "Flash Next"}, tierNano: {"Bolt", "MiMo"}, tierPico: {"LFM3", "MiniCPM5"}, 0: nil,
	} {
		if got := names(lineupFor(tier, "linux/amd64", "0.40.17")); !slices.Equal(got, want) {
			t.Errorf("%s offers %v, want %v", cmp.Or(tierName(tier), "below pico"), got, want)
		}
	}
}

func TestTheRowOffersInklingWhereTheHostOllamaServesIt(t *testing.T) {
	stubOllamaHost(t, "darwin/arm64", "0.40.17")
	r, _ := modelRow(hermesImagesMan(), "", tierMini)
	if want := []string{modelAPIKeys, "Inkling", "Qwen 3.8"}; !slices.Equal(r.Options, want) || !slices.Equal(r.Cols, []int{0, 2, 3}) {
		t.Errorf("options = %v cols = %v, want %v in columns 0, 2, 3", r.Options, r.Cols, want)
	}
	tag := localModelFor("Inkling")
	if tag != "proveo/inkling-2-mini-uncensored:26b-a5b-nvfp4" {
		t.Errorf("Inkling → --local-model %q, want the served MLX import, not the dormant GGUF", tag)
	}
	if imp, ok := sbx.ImportFor(tag); !ok || imp.Repo != "OpenUncensored/Inkling-2-Mini-Uncensored" {
		t.Errorf("%s imports %+v (%v), want OpenUncensored's pinned files", tag, imp.Repo, ok)
	}
	if h := r.Help["Inkling"]; !strings.Contains(h, "(OpenUncensored), an MLX import, MLX") {
		t.Errorf("hint = %q, want the served build's lead and MLX", h)
	}

	stubOllamaHost(t, "darwin/arm64", "0.40.16")
	r, _ = modelRow(hermesImagesMan(), "inkling-2-mini:26b-a5b-nvfp4", tierMini)
	if want := []string{modelAPIKeys, "Inkling", "Qwen 3.8"}; !slices.Equal(r.Options, want) || r.Options[r.Selected] != "Inkling" {
		t.Errorf("a typed official Inkling: options = %v selected %q, want %v with Inkling kept", r.Options, r.Options[r.Selected], want)
	}
}

func TestARememberedPresetFollowsItsColumnsPick(t *testing.T) {
	none := func(string) string { return "" }
	for remembered, want := range map[string]string{
		"qwen3.8:27b-mlx":              qwen.tag(),
		"qwen3.8:latest":               qwen.tag(),
		"inkling-2-mini:26b-a5b-nvfp4": "inkling-2-mini:26b-a5b-nvfp4",
	} {
		var p Params
		p.seedFromCache(agentsettings.Choice{LocalModel: remembered}, none, false)
		if p.LocalModel != want {
			t.Errorf("a remembered %s = %q, want %q (a column with no servable pick keeps the tag)", remembered, p.LocalModel, want)
		}
	}

	stubOllamaHost(t, "darwin/arm64", "0.40.17")
	var p Params
	p.seedFromCache(agentsettings.Choice{LocalModel: "inkling-2-mini:26b-a5b-nvfp4"}, none, false)
	if want := "proveo/inkling-2-mini-uncensored:26b-a5b-nvfp4"; p.LocalModel != want {
		t.Errorf("a remembered official Inkling once the import is servable = %q, want %q", p.LocalModel, want)
	}
	typed := Params{LocalModel: "inkling-2-mini:26b-a5b-nvfp4"}
	typed.seedFromCache(agentsettings.Choice{LocalModel: "qwen3.8:27b-mlx"}, none, false)
	if typed.LocalModel != "inkling-2-mini:26b-a5b-nvfp4" {
		t.Errorf("a typed official Inkling must stay as typed, got %q", typed.LocalModel)
	}
}

func TestTheRowNeverDrawsMoreThanTwoLocalModels(t *testing.T) {
	tags := []string{"", "gemma4:12b", "hf.co/someone/tiny-GGUF:Q4_K_M"}
	for _, m := range localModels {
		tags = append(tags, m.tagFor("darwin/arm64"), m.tagFor("linux/amd64"))
	}
	for _, host := range [][2]string{{"darwin/arm64", "0.40.17"}, {"darwin/arm64", "0.40.16"}, {"linux/amd64", ""}} {
		stubOllamaHost(t, host[0], host[1])
		for _, tier := range []int{0, tierPico, tierNano, tierMini, tierSmall} {
			for _, tag := range tags {
				r, _ := modelRow(hermesImagesMan(), tag, tier)
				local := slices.DeleteFunc(slices.Clone(r.Options), func(o string) bool { return o == modelAPIKeys })
				cols := slices.Clone(r.Cols[1:])
				slices.Sort(cols)
				if len(local) > 2 || len(slices.Compact(cols)) != len(cols) {
					t.Errorf("%s Ollama %q tier %d tag %q: options %v cols %v, want at most two local models in distinct columns",
						host[0], host[1], tier, tag, r.Options, r.Cols)
				}
				if tag != "" && r.Options[r.Selected] != modelLabelFor(tag) {
					t.Errorf("%s tier %d tag %q selected %q, want %q", host[0], tier, tag, r.Options[r.Selected], modelLabelFor(tag))
				}
			}
		}
	}
}

func TestAnUnlistedTagReplacesThePair(t *testing.T) {
	t.Parallel()
	for _, tier := range []int{tierMini, tierSmall} {
		r, _ := modelRow(hermesImagesMan(), "gemma4:12b", tier)
		if want := []string{modelAPIKeys, "gemma4:12b"}; !slices.Equal(r.Options, want) || !slices.Equal(r.Cols, []int{0, 2}) {
			t.Errorf("tier %d: options = %v cols = %v, want %v in columns 0, 2", tier, r.Options, r.Cols, want)
		}
	}
}

func TestInklingOwnsItsLibraryAndHuggingFaceBuilds(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{"inkling-2-mini", "inkling-2-mini:latest", "inkling-2-mini:26b-a5b-nvfp4",
		"hf.co/AtomicChat/Inkling-2-Mini-Uncensored-GGUF:IQ3_S", "proveo/inkling-2-mini-uncensored:26b-a5b-nvfp4"} {
		if got := modelLabelFor(tag); got != "Inkling" {
			t.Errorf("%s maps to %q, want Inkling", tag, got)
		}
	}
	for _, tag := range []string{"inkling-2-small:latest", "hf.co/unsloth/Inkling-2-Mini-GGUF:UD-Q4_K_M"} {
		if inkling.owns(tag) || inklingMLX.owns(tag) {
			t.Errorf("%s is not a listed Inkling build", tag)
		}
	}
	for tag, label := range map[string]string{
		"hf.co/huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF:Q5_K_M": "Qwen 3.8",
		"hf.co/Vortecks/LFM3-1.7B-Heretic-Abliterated-GGUF:Q4_K_M":   "LFM3",
		"proveo/nemotron-3.5-super-heretic:120b-a12b-i1-q4_k_m":      "Nemotron",
	} {
		if got := modelLabelFor(tag); got != label {
			t.Errorf("%s maps to %q, want %q", tag, got, label)
		}
	}
}

func TestMemoryRangesAreNamedByTheirFloor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		gib  uint64
		tier int
		name string
	}{
		{2, 0, ""}, {5, 0, ""}, {6, tierPico, "pico"}, {7, tierPico, "pico"}, {8, tierNano, "nano"}, {23, tierNano, "nano"},
		{24, tierMini, "mini"}, {95, tierMini, "mini"}, {96, tierSmall, "small"}, {512, tierSmall, "small"},
	} {
		tier := modelTier(c.gib << 30)
		if tier != c.tier || tierName(tier) != c.name {
			t.Errorf("%d GiB = tier %d %q, want %d %q", c.gib, tier, tierName(tier), c.tier, c.name)
		}
	}
}

func TestEveryPresetIsAFreshHuggingFaceBuild(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, m := range localModels {
		if seen[m.repo] {
			t.Errorf("repo id %q is shared; the served-first label lookup keys on it", m.repo)
		}
		seen[m.repo] = true
		age, ok := m.presetAge()
		switch {
		case !ok:
			t.Errorf("%s (%s): released %q is not YYYY-MM-DD", m.name, m.repo, m.released)
		case age > presetForbidAge:
			t.Errorf("%s (%s): released %s, over 6 months old; refresh the preset", m.name, m.repo, m.released)
		case age > presetWarnAge:
			t.Logf("warning: %s (%s) was released %s, over 3 months old", m.name, m.repo, m.released)
		}
		for platform, tag := range m.tags {
			if imp, ok := m.imports[platform]; ok {
				if imp.Repo == "" || len(imp.Revision) != 40 {
					t.Errorf("%s on %q: import %q@%q is not a pinned Hugging Face revision", m.name, platform, imp.Repo, imp.Revision)
				}
				continue
			}
			if !strings.HasPrefix(tag, "hf.co/") {
				t.Errorf("%s on %q pulls %s, want an hf.co/ Hugging Face source", m.name, platform, tag)
			}
		}
		if !m.abliterated {
			t.Logf("note: %s (%s) is not abliterated", m.name, m.repo)
		}
	}
}

func TestTheHintWarnsPastThreeMonths(t *testing.T) {
	orig := presetNow
	t.Cleanup(func() { presetNow = orig })
	presetNow = func() time.Time { return time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC) }
	if h := qwen.hintLead(); !strings.Contains(h, "released 2026-08-05, over 3 months old") {
		t.Errorf("Qwen hint = %q, want the 3-month warning", h)
	}
	if h := bolt.hintLead(); strings.Contains(h, "over 3 months") {
		t.Errorf("Bolt hint = %q, want no warning at 29 days", h)
	}
}
