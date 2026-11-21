// SPEC: _spec/internal/choiceui/wireframe.puml, _spec/defs/hermes/hermes-paradigm.puml
package run

import (
	"cmp"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/credentials"
	"github.com/proveo-ca/proveo/internal/manifest"
	"github.com/proveo-ca/proveo/internal/sbx"
)

const (
	rowModel     = "model"
	modelAPIKeys = "API keys"
	modelPlan    = "plan"
)

// Memory ranges, by the least installed GiB each starts at.
const (
	tierSmall = 96 // small: 96 GB and up
	tierMini  = 24 // mini: 24 to 96 GB
	tierNano  = 8  // nano: 8 to 24 GB
	tierPico  = 6  // pico: 6 to 8 GB
)

// memoryRanges orders the ranges from the largest floor down.
var memoryRanges = []struct {
	floor int
	name  string
}{{tierSmall, "small"}, {tierMini, "mini"}, {tierNano, "nano"}, {tierPico, "pico"}}

// modelTier is the floor of the range capacity falls in; 0 below pico.
func modelTier(capacity uint64) int {
	for _, r := range memoryRanges {
		if capacity >= uint64(r.floor)<<30 {
			return r.floor
		}
	}
	return 0
}

// tierName names the range a tier floor starts; "" for none.
func tierName(tier int) string {
	for _, r := range memoryRanges {
		if r.floor == tier {
			return r.name
		}
	}
	return ""
}

// ollamaUnreleased is a floor no Ollama release meets yet.
const ollamaUnreleased = "unreleased"

// localModel is one row choice: repo and its tags' repos claim a tag, tags and builds pick this host's build,
// floors gate it on the host's Ollama version, imports build a platform's tag from Hugging Face files.
type localModel struct {
	name, repo, short, size string // short is the topology box; size is the hint, not the cell
	lead                    string // the hint's opening words
	released                string // the base model's release, YYYY-MM-DD
	abliterated             bool
	tags                    map[string]string
	builds                  map[string]string
	floors                  map[string]string // "" any version; ollamaUnreleased none yet
	imports                 map[string]sbx.Import
	tier, column            int
}

// localModels are the row's local choices; per tier and column the first one the host's Ollama serves wins.
var localModels = []localModel{
	{name: "Nemotron", repo: "nemotron-3.5-super", short: "nemotron", size: "120B-A12B",
		lead: "Nemotron 3.5 Super 120B-A12B heretic (spiritfather), a ~80 GiB import", released: "2026-10-12", abliterated: true,
		tags:    map[string]string{"": "proveo/nemotron-3.5-super-heretic:120b-a12b-i1-q4_k_m"},
		builds:  map[string]string{"": "GGUF"},
		imports: map[string]sbx.Import{"": importNemotronHeretic}, tier: tierSmall, column: 2},
	{name: "Flash Next", repo: "qwen3.8-flash-next", short: "flash-next", size: "125B-A6B",
		lead: "Qwen3.8-Flash-Next 125B-A6B abliterated v2 (huihui), a ~87 GiB import", released: "2026-08-24", abliterated: true,
		tags:    map[string]string{"": "proveo/qwen3.8-flash-next-abliterated:125b-a6b-ud-iq4_xs"},
		builds:  map[string]string{"": "GGUF"},
		imports: map[string]sbx.Import{"": importFlashNextAbliterated}, tier: tierSmall, column: 3},
	{name: "Inkling", repo: "inkling-2-mini", short: "inkling", size: "26B-A5B",
		lead: "Inkling 2 Mini 26B-A5B uncensored (AtomicChat)", released: "2026-11-20", abliterated: true,
		tags:   map[string]string{"": "hf.co/AtomicChat/Inkling-2-Mini-Uncensored-GGUF:IQ3_S"},
		builds: map[string]string{"": "GGUF"},
		floors: map[string]string{"": ollamaUnreleased}, tier: tierMini, column: 2},
	{name: "Inkling", repo: "inkling-2-mini-mlx", short: "inkling", size: "26B-A5B",
		lead: "Inkling 2 Mini 26B-A5B uncensored (OpenUncensored), an MLX import", released: "2026-11-20", abliterated: true,
		tags:    map[string]string{"darwin/arm64": "proveo/inkling-2-mini-uncensored:26b-a5b-nvfp4"},
		builds:  map[string]string{"darwin/arm64": "MLX"},
		floors:  map[string]string{"darwin/arm64": "0.40.17", "": ollamaUnreleased},
		imports: map[string]sbx.Import{"darwin/arm64": importInklingUncensored}, tier: tierMini, column: 2},
	{name: "Qwen 3.8", repo: "qwen3.8", short: "qwen3.8", size: "27B",
		lead: "Qwen 3.8 27B abliterated (huihui)", released: "2026-08-05", abliterated: true,
		tags:   map[string]string{"": "hf.co/huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF:Q4_K_M"},
		builds: map[string]string{"": "GGUF"}, tier: tierMini, column: 3},
	{name: "Bolt", repo: "nemotron-3.5-bolt", short: "bolt", size: "E4B",
		lead: "Nemotron 3.5 Bolt E4B heretic (mradermacher)", released: "2026-10-23", abliterated: true,
		tags:   map[string]string{"": "hf.co/mradermacher/NVIDIA-Nemotron-3.5-Bolt-E4B-BF16-heretic-3-i1-GGUF:i1-Q4_K_M"},
		builds: map[string]string{"": "GGUF"}, tier: tierNano, column: 2},
	{name: "MiMo", repo: "mimo-v2.6", short: "mimo", size: "9B",
		lead: "MiMo-V2.6 Distill Qwen 9B abliterated (ARA)", released: "2026-09-21", abliterated: true,
		tags:   map[string]string{"": "hf.co/mradermacher/MiMo-V2.6-Distill-Qwen-9B-ARA-Abliterated-i1-GGUF:i1-IQ4_XS"},
		builds: map[string]string{"": "GGUF"}, tier: tierNano, column: 3},
	{name: "LFM3", repo: "lfm3", short: "lfm3", size: "1.7B",
		lead: "LFM3 1.7B heretic (Vortecks), Q8_0", released: "2026-10-23", abliterated: true,
		tags:   map[string]string{"": "hf.co/Vortecks/LFM3-1.7B-Heretic-Abliterated-GGUF:Q8_0"},
		builds: map[string]string{"": "GGUF"}, tier: tierPico, column: 2},
	{name: "MiniCPM5", repo: "minicpm5", short: "minicpm5", size: "2B",
		lead: "MiniCPM5 2B heretic (Abiray), Q6_K", released: "2026-09-06", abliterated: true,
		tags:   map[string]string{"": "hf.co/Abiray/MiniCPM5-2B-heretic-abliterated-GGUF:Q6_K"},
		builds: map[string]string{"": "GGUF"}, tier: tierPico, column: 3},
}

// Pinned Hugging Face imports: a revision and each large file's LFS sha256.
var (
	importNemotronHeretic = sbx.Import{Repo: "spiritfather/NVIDIA-Nemotron-3.5-Super-VL-120B-A12B-heretic-ara-i1-GGUF",
		Revision: "28261d680b80c4c5c2724d5f0a3ebb74e2f41b4f", Files: []sbx.ImportFile{
			{Path: "i1-Q4_K_M/NVIDIA-Nemotron-3.5-Super-VL-120B-A12B-heretic-ara.i1-Q4_K_M-00001-of-00002.gguf",
				SHA256: "4f11337644f7df8f0d61f72f9816c89c461a8b53cc341cdcac572bbf7347d053", Size: 44456972480},
			{Path: "i1-Q4_K_M/NVIDIA-Nemotron-3.5-Super-VL-120B-A12B-heretic-ara.i1-Q4_K_M-00002-of-00002.gguf",
				SHA256: "f8a5f340a07a99820e149852eb6b6c870dd58b7a06332632778340559691ff19", Size: 41594110368},
		}}
	importFlashNextAbliterated = sbx.Import{Repo: "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
		Revision: "cbc7fba13f1939ac3fa42b18d77ccf43847d74ef", Files: []sbx.ImportFile{
			{Path: "unsloth-UD-IQ4_XS/Qwen3.8-Flash-Next-UD-IQ4_XS-00001-of-00003.gguf",
				SHA256: "5ce89370720f8bf90890f439361282104c1aa1482d4013bb9a50923e758e71a4", Size: 10946624},
			{Path: "unsloth-UD-IQ4_XS/Qwen3.8-Flash-Next-UD-IQ4_XS-00002-of-00003.gguf",
				SHA256: "ede87d3bd3e354d43f0e710b2df6791d3479e396847e53adbe2b48be7ba6d663", Size: 49835229856},
			{Path: "unsloth-UD-IQ4_XS/Qwen3.8-Flash-Next-UD-IQ4_XS-00003-of-00003.gguf",
				SHA256: "f8aa8bd7ba134b3b7fe4fc95b9131fc8f6678ace374a0fa6e632225031665995", Size: 43836407744},
		}}
	importInklingUncensored = sbx.Import{Repo: "OpenUncensored/Inkling-2-Mini-Uncensored",
		Revision: "4e5f06d72b5a70c9dcd5e8046aea576eb3ee8487", Files: []sbx.ImportFile{
			{Path: "chat_template.jinja", Size: 6868},
			{Path: "config.json", Size: 2699},
			{Path: "generation_config.json", Size: 98},
			{Path: "hf_quant_config.json", Size: 67924},
			{Path: "model-00001-of-00001.safetensors", SHA256: "a841d90643aec49a9dd07968ee96a26fe0a9d829d0f27901ebb5cb3938c99227", Size: 19526019168},
			{Path: "model.safetensors.index.json", Size: 109300},
			{Path: "mtp.safetensors", SHA256: "7c3b651317f01f73bcf4e1dceba66ef8b7e1a1eac6bc171e882e690a4b10a1d2", Size: 635801416},
			{Path: "processor_config.json", Size: 461},
			{Path: "special_tokens_map.json", Size: 529},
			{Path: "tiktoken/tokenizer.model", SHA256: "f170372dd655728909228220d77cf6b8104a59f38a91262a7d2c4027906ec0d8", Size: 3386494},
			{Path: "tokenizer.json", SHA256: "7f332727f9a3f093dd452a39cc978e6757627e5dad1b0673aa4e75174f236dd6", Size: 26178678},
			{Path: "tokenizer_config.json", Size: 13235},
		}}
)

func init() {
	for _, m := range localModels {
		for platform, imp := range m.imports {
			sbx.RegisterImport(m.tagFor(platform), imp)
		}
	}
}

// hostOllamaVersion is the host Ollama's version, read once per process.
var hostOllamaVersion = sync.OnceValue(sbx.HostOllamaVersion)

func forPlatform(m map[string]string, platform string) string {
	if v, ok := m[platform]; ok {
		return v
	}
	return m[""]
}

func (m localModel) labelFor(string) string { return m.name }

func (m localModel) buildFor(platform string) string { return forPlatform(m.builds, platform) }

func (m localModel) label() string { return m.labelFor(ollamaPlatform()) }

// ollamaPlatform is the host as the tags and floors key it.
var ollamaPlatform = func() string { return runtime.GOOS + "/" + runtime.GOARCH }

// tagFor is the Ollama tag for platform.
func (m localModel) tagFor(platform string) string { return forPlatform(m.tags, platform) }

func (m localModel) tag() string { return m.tagFor(ollamaPlatform()) }

// servedBy reports whether an Ollama at version on platform serves this model.
func (m localModel) servedBy(platform, version string) bool {
	switch floor := forPlatform(m.floors, platform); floor {
	case "":
		return true
	case ollamaUnreleased:
		return false
	default:
		return ollamaAtLeast(version, floor)
	}
}

// ollamaAtLeast compares x.y.z releases; a pre-release counts as its release, an unparsable version as too old.
func ollamaAtLeast(version, floor string) bool {
	v, okV := ollamaRelease(version)
	f, okF := ollamaRelease(floor)
	return okV && okF && slices.Compare(v, f) >= 0
}

func ollamaRelease(version string) ([]int, bool) {
	core, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nil, false
	}
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// lineupFor is what the row offers at tier: per column, the first model platform's Ollama at version serves.
func lineupFor(tier int, platform, version string) []localModel {
	var out []localModel
	taken := map[int]bool{}
	for _, m := range localModels {
		if m.tier != tier || taken[m.column] || !m.servedBy(platform, version) {
			continue
		}
		taken[m.column] = true
		out = append(out, m)
	}
	return out
}

func lineup(tier int) []localModel { return lineupFor(tier, ollamaPlatform(), hostOllamaVersion()) }

// preferred lists every preset, the ones this host's lineups serve first, so a label two builds share names the served one.
func preferred() []localModel {
	var out []localModel
	seen := map[string]bool{}
	add := func(m localModel) {
		if !seen[m.repo] {
			seen[m.repo] = true
			out = append(out, m)
		}
	}
	for _, r := range memoryRanges {
		for _, m := range lineup(r.floor) {
			add(m)
		}
	}
	for _, m := range localModels {
		add(m)
	}
	return out
}

// repoOf strips the tag from an Ollama model reference.
func repoOf(ref string) string {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// owns reports whether tag is any build of this model.
func (m localModel) owns(tag string) bool {
	for _, r := range append([]string{m.repo}, slices.Collect(maps.Values(m.tags))...) {
		if r = repoOf(r); tag == r || strings.HasPrefix(tag, r+":") {
			return true
		}
	}
	return false
}

func modelHelp(localModel string) map[string]string {
	h := map[string]string{
		modelAPIKeys: "no local model: the agent uses an external LLM provider through your keys, brokered like any usage key",
	}
	for _, m := range preferred() {
		if _, set := h[m.label()]; !set {
			h[m.label()] = modelHint(m, "same as --local-model "+m.tag()+" on "+ollamaPlatform()+": the host's Ollama serves it, no key")
		}
	}
	claimed := false
	for _, m := range localModels {
		if m.owns(localModel) {
			claimed = true
			if localModel != m.tag() {
				h[m.label()] = modelHint(m, "keeps your --local-model "+localModel+" (the row picks "+m.tag()+" on "+ollamaPlatform()+")")
			}
			break
		}
	}
	if localModel != "" && !claimed {
		h[localModel] = "from --local-model: the host's Ollama serves it, no key"
	}
	return h
}

// presetNow is the clock the age hint reads.
var presetNow = time.Now

// presetAge is how long ago m's base model was released; ok is false for an unparsable date.
func (m localModel) presetAge() (time.Duration, bool) {
	t, err := time.Parse(time.DateOnly, m.released)
	return presetNow().Sub(t), err == nil
}

const (
	presetWarnAge   = 92 * 24 * time.Hour  // three months
	presetForbidAge = 183 * 24 * time.Hour // six months
)

func (m localModel) hintLead() string {
	lead := cmp.Or(m.lead, m.name)
	if age, ok := m.presetAge(); ok && age > presetWarnAge {
		lead += " · released " + m.released + ", over 3 months old"
	}
	return lead
}

func modelHint(m localModel, rest string) string {
	lead := m.hintLead()
	if b := m.buildFor(ollamaPlatform()); b != "" {
		lead += ", " + b
	}
	return lead + ", " + rest
}

// noLocalModel names the harnesses whose inference is vendor-pinned.
var noLocalModel = map[string]bool{"cursor": true}

// acceptsLocalModel reports whether target can run on --local-model.
func acceptsLocalModel(target string) bool { return !noLocalModel[target] }

// modelFlags maps a harness to the native flag its agent takes for a remote provider/model.
var modelFlags = map[string]string{"opencode": "-m", "codex": "--model", "claudecode": "--model"}

// acceptsRemoteModel reports the passthrough flag a remote model spec takes on target.
func acceptsRemoteModel(target string) (string, bool) {
	flag, ok := modelFlags[target]
	return flag, ok
}

// applyRemoteModel routes --model onto the harness's native flag through the agent passthrough.
func applyRemoteModel(p *Params) error {
	if p.Model == "" {
		return nil
	}
	flag, ok := acceptsRemoteModel(p.Target)
	if !ok {
		return fmt.Errorf("%s has no --model path (inference is vendor-pinned or not wired); unset it or use another harness", p.Target)
	}
	end := slices.Index(p.Extra, "--")
	if end < 0 {
		end = len(p.Extra)
	}
	p.Extra = slices.Insert(p.Extra, end, flag, p.Model)
	return nil
}

// modelColumn maps options to the shared grid; an unlisted tag takes the first local column.
func modelColumn(opt string) int {
	switch opt {
	case modelAPIKeys:
		return 0
	case credentials.AuthSubscription, modelPlan:
		return 1
	}
	for _, m := range localModels {
		if opt == m.label() {
			return m.column
		}
	}
	return 2
}

// localPicks is the row's local options, at most one per column: tag's preset takes its column's slot,
// an unlisted tag replaces the lineup.
func localPicks(tag string, tier int) []string {
	offered := lineup(tier)
	if tag == "" {
		return labels(offered)
	}
	for _, m := range localModels {
		if m.owns(tag) {
			kept := slices.DeleteFunc(offered, func(o localModel) bool { return o.column == m.column })
			return append(labels(kept), m.label())
		}
	}
	return []string{tag}
}

func labels(ms []localModel) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.label()
	}
	return out
}

// modelRow is the single-select model row, drawn for every harness that accepts --local-model.
func modelRow(man manifest.Manifest, localModel string, tier int) (choiceui.Row, bool) {
	if !acceptsLocalModel(man.Name) {
		return choiceui.Row{}, false
	}
	r := axisRow(rowModel, append([]string{modelAPIKeys}, localPicks(localModel, tier)...), nil, modelLabelFor(localModel))
	r.Help = modelHelp(localModel)
	sortModelRow(&r, tier)
	return r, true
}

func hasModelRow(man manifest.Manifest) bool {
	return acceptsLocalModel(man.Name)
}

// localModelFor is the --local-model value a row option stands for; "" means API keys.
func localModelFor(option string) string {
	if option == modelAPIKeys {
		return ""
	}
	for _, m := range preferred() {
		if m.label() == option {
			return m.tag()
		}
	}
	return option
}

// modelLabelFor is the row option a --local-model value preselects.
func modelLabelFor(localModel string) string {
	if localModel == "" {
		return modelAPIKeys
	}
	for _, m := range localModels {
		if m.owns(localModel) {
			return m.label()
		}
	}
	return localModel
}

// hostTagFor maps a remembered tag of a listed model to this host's build of its column's pick; other tags pass through.
func hostTagFor(tag string) string {
	for _, m := range localModels {
		if !m.owns(tag) {
			continue
		}
		if m.tier == tierMini || tag == m.tagFor("") || tag == m.tagFor("darwin/arm64") {
			if pick, ok := columnPick(m); ok {
				return pick.tag()
			}
			return tag
		}
	}
	return tag
}

// columnPick is the model this host's lineup offers in m's tier and column; ok is false when it offers none.
func columnPick(m localModel) (localModel, bool) {
	for _, o := range lineup(m.tier) {
		if o.column == m.column {
			return o, true
		}
	}
	return m, false
}

// legacyVariantTag maps a cached modelVariant image key to its --local-model value.
func legacyVariantTag(variant string) string {
	for _, m := range localModels {
		if variant == "hermes-"+m.repo {
			if pick, ok := columnPick(m); ok {
				return pick.tag()
			}
			return m.tag()
		}
	}
	return ""
}

// sourceRow is the one row that picks where the model comes from: provider keys, the harness's plan, or a local model.
func sourceRow(man manifest.Manifest, auth choiceui.Row, hasAuth bool, localModel string, tier int) (choiceui.Row, bool) {
	local, hasLocal := modelRow(man, localModel, tier)
	if !hasAuth {
		return local, hasLocal
	}
	r := auth
	r.Label = rowModel
	renameOption(&r, credentials.AuthUsage, modelAPIKeys)
	renameOption(&r, credentials.AuthSubscription, modelPlan)
	if !hasLocal {
		sortModelRow(&r, tier)
		return r, true
	}
	if r.Help == nil {
		r.Help = map[string]string{}
	}
	for len(r.Off) < len(r.Options) {
		r.Off = append(r.Off, false)
	}
	for _, opt := range local.Options {
		if opt == modelAPIKeys {
			continue
		}
		r.Options = append(r.Options, opt)
		r.Off = append(r.Off, false)
		r.Help[opt] = local.Help[opt]
	}
	if localModel != "" {
		if i := slices.Index(r.Options, modelLabelFor(localModel)); i >= 0 {
			r.Selected = i
		}
	}
	sortModelRow(&r, tier)
	return r, true
}

// sortModelRow orders options by column, carrying each option's gate with it.
func sortModelRow(r *choiceui.Row, tier int) {
	chosen := ""
	if r.Selected >= 0 && r.Selected < len(r.Options) {
		chosen = r.Options[r.Selected]
	}
	type item struct {
		opt string
		off bool
	}
	items := make([]item, len(r.Options))
	for i, opt := range r.Options {
		items[i] = item{opt, i < len(r.Off) && r.Off[i]}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		return cmp.Compare(modelColumn(a.opt), modelColumn(b.opt))
	})
	for i, it := range items {
		r.Options[i] = it.opt
		if i < len(r.Off) {
			r.Off[i] = it.off
		}
	}
	if i := slices.Index(r.Options, chosen); i >= 0 {
		r.Selected = i
	}
	pinModelCols(r, tier)
}

func pinModelCols(r *choiceui.Row, tier int) {
	r.Cols = make([]int, len(r.Options))
	for i, opt := range r.Options {
		r.Cols[i] = modelColumn(opt)
	}
	if len(r.Cols) == 0 {
		return
	}
	first, last := r.Cols[0], r.Cols[len(r.Cols)-1]
	if first < 2 {
		r.ColumnGroups = append(r.ColumnGroups, choiceui.ColumnGroup{Label: "cloud", First: first, Last: min(last, 1)})
	}
	if last >= 2 {
		label := "local"
		switch {
		case tier > 0 && ollamaPlatform() == "darwin/arm64":
			label = fmt.Sprintf("local (%d GB unified)", tier)
		case tier == tierSmall:
			label = "local (96 GB VRAM tier)"
		case tier == tierMini:
			label = "local (needs 24 GB VRAM)"
		}
		r.ColumnGroups = append(r.ColumnGroups, choiceui.ColumnGroup{Label: label, First: max(first, 2), Last: last})
	}
}

func renameOption(r *choiceui.Row, from, to string) {
	i := slices.Index(r.Options, from)
	if i < 0 {
		return
	}
	r.Options = slices.Clone(r.Options)
	r.Options[i] = to
	if h, ok := r.Help[from]; ok {
		r.Help[to] = h
		delete(r.Help, from)
	}
	if w, ok := r.OffWhy[from]; ok {
		r.OffWhy[to] = w
		delete(r.OffWhy, from)
	}
	r.Reason = strings.ReplaceAll(r.Reason, from+": ", to+": ")
}

// isLocalOption reports whether a source-row option names a local model.
func isLocalOption(v string) bool {
	return v != modelAPIKeys && v != modelPlan && !credentials.IsAuthSentinel(v)
}

// applySource records a source-row answer as --local-model and, on a harness with a plan, the auth answer.
func (p *Params) applySource(v string, hasAuth bool) {
	if isLocalOption(v) {
		if v != modelLabelFor(p.LocalModel) {
			p.LocalModel = localModelFor(v)
		}
		if hasAuth {
			p.AuthVar = credentials.AuthLocal
		}
		return
	}
	p.LocalModel = ""
	switch {
	case v == modelAPIKeys && hasAuth:
		p.AuthVar = credentials.AuthUsage
	case v == modelPlan:
		p.AuthVar = credentials.AuthSubscription
	case v != modelAPIKeys:
		p.AuthVar = v
	}
}

// localBesideHost is the name drawn under the host when option is a local model.
func localBesideHost(option string) (string, bool) {
	if option == "" || !isLocalOption(option) {
		return "", false
	}
	for _, m := range preferred() {
		if m.label() == option {
			return m.label(), true
		}
	}
	return option, true
}

// modelShort is what the topology box prints for a model-row answer; "" for API keys.
func modelShort(option string) string {
	switch option {
	case "", modelAPIKeys, credentials.AuthUsage:
		return ""
	case credentials.AuthSubscription, modelPlan:
		return "plan"
	}
	for _, m := range preferred() {
		if m.label() == option {
			return m.short
		}
	}
	return option
}
