package main

import (
	"fmt"
	"maps"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	meilisearchv1 "github.com/pckt-sh/protokv/gen/go/protokv/meilisearch/v1"
	"github.com/pckt-sh/protokv/kv"
	"github.com/pckt-sh/protokv/schema"
	"google.golang.org/protobuf/proto"
)

type focus int

const (
	focusSidebar focus = iota
	focusMain
)

type inputMode int

const (
	inputNone inputMode = iota
	inputFilter
	inputOptions
)

// level is one page of the navigation stack: the keys of a model.
type level struct {
	title   string
	storage schema.Storage
	filter  string

	keys    []string
	cursor  int
	offset  int
	scan    uint64
	done    bool
	loading bool
	err     error

	// fixed levels list given keys, they aren't scanned.
	fixed bool
	// gen discards pages of a previous scan after a reload.
	gen int
}

// sheet is the modal showing a record and its relations.
type sheet struct {
	level   *level
	key     string
	tabs    []tab
	active  int
	loading bool
	vp      viewport.Model
}

type pageMsg struct {
	level *level
	gen   int
	page  page
	err   error
}

type recordMsg struct {
	sheet *sheet
	tabs  []tab
}

type app struct {
	db     kv.Client
	url    string
	models []schema.Storage
	st     styles

	// options fill key placeholders, shared by every model.
	options map[string]string

	selected int
	focus    focus
	stack    []*level
	sheet    *sheet

	mode  inputMode
	input textinput.Model

	width, height int
}

func newApp(db kv.Client, url string, models []schema.Storage) *app {
	input := textinput.New()
	input.Prompt = ""

	inputStyles := input.Styles()
	inputStyles.Cursor.Blink = false
	input.SetStyles(inputStyles)

	a := &app{
		db:     db,
		url:    url,
		models: models,
		st:     newStyles(),
		input:  input,

		options: defaultOptions(models),
	}

	return a
}

func (a *app) Init() tea.Cmd {
	if len(a.models) == 0 {
		return nil
	}

	return a.openModel(0)
}

// openModel resets the navigation to the keys of a model.
func (a *app) openModel(idx int) tea.Cmd {
	a.selected = idx
	s := a.models[idx]

	lvl := &level{title: shortName(s.GetName()), storage: s}
	a.stack = []*level{lvl}

	return a.reload(lvl)
}

// defaultOptions merges the Meilisearch options of the models: they
// are the values used to load relations, a good default for the
// placeholders too. The first model setting an option wins.
func defaultOptions(models []schema.Storage) map[string]string {
	options := map[string]string{}
	for _, s := range models {
		opts := s.GetDescriptor().Options()
		if !proto.HasExtension(opts, meilisearchv1.E_Index) {
			continue
		}

		for k, v := range proto.GetExtension(opts, meilisearchv1.E_Index).(*meilisearchv1.Index).GetOptions() {
			if _, ok := options[k]; !ok {
				options[k] = v
			}
		}
	}

	return options
}

func (a *app) current() *level {
	if len(a.stack) == 0 {
		return nil
	}

	return a.stack[len(a.stack)-1]
}

func (a *app) reload(lvl *level) tea.Cmd {
	lvl.gen++
	lvl.keys, lvl.cursor, lvl.offset, lvl.scan = nil, 0, 0, 0
	lvl.done, lvl.err = false, nil

	if lvl.fixed {
		lvl.done = true
		return nil
	}

	return a.loadPage(lvl)
}

func (a *app) loadPage(lvl *level) tea.Cmd {
	if lvl.loading || lvl.done {
		return nil
	}
	lvl.loading = true

	var (
		db                         = a.db
		s, options, filter, cursor = lvl.storage, maps.Clone(a.options), lvl.filter, lvl.scan
		gen                        = lvl.gen
	)

	return func() tea.Msg {
		p, err := scanPage(db, s, options, filter, cursor)
		return pageMsg{level: lvl, gen: gen, page: p, err: err}
	}
}

func (a *app) openSheet(lvl *level, key string) tea.Cmd {
	sh := &sheet{level: lvl, key: key, loading: true, vp: viewport.New()}
	sh.vp.SoftWrap = true
	a.sheet = sh
	a.resizeSheet()

	db, s, options := a.db, lvl.storage, maps.Clone(a.options)
	return func() tea.Msg {
		return recordMsg{sheet: sh, tabs: loadRecord(db, s, key, options)}
	}
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resizeSheet()
		return a, nil

	case pageMsg:
		lvl := msg.level
		if msg.gen != lvl.gen {
			return a, nil
		}

		lvl.loading = false
		if msg.err != nil {
			lvl.err = msg.err
			lvl.done = true
			return a, nil
		}

		lvl.keys = append(lvl.keys, msg.page.keys...)
		lvl.scan = msg.page.cursor
		lvl.done = msg.page.done

		// Keep loading until the list fills the screen.
		if !lvl.done && len(lvl.keys) < lvl.cursor+a.listHeight() {
			return a, a.loadPage(lvl)
		}
		return a, nil

	case recordMsg:
		if msg.sheet != a.sheet {
			return a, nil
		}

		a.sheet.loading = false
		a.sheet.tabs = msg.tabs
		a.renderTab()
		return a, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}

		switch {
		case a.mode != inputNone:
			return a, a.updateInput(msg)
		case a.sheet != nil:
			return a, a.updateSheet(msg)
		case a.focus == focusSidebar:
			return a, a.updateSidebar(msg)
		default:
			return a, a.updateMain(msg)
		}
	}

	if a.mode != inputNone {
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		return a, cmd
	}

	return a, nil
}

func (a *app) updateSidebar(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		return tea.Quit
	case "up", "k":
		if a.selected > 0 {
			return a.openModel(a.selected - 1)
		}
	case "down", "j":
		if a.selected < len(a.models)-1 {
			return a.openModel(a.selected + 1)
		}
	case "enter", "right", "l", "tab":
		if len(a.models) > 0 {
			a.focus = focusMain
		}
	}

	return nil
}

func (a *app) updateMain(msg tea.KeyPressMsg) tea.Cmd {
	lvl := a.current()
	if lvl == nil {
		a.focus = focusSidebar
		return nil
	}

	switch msg.String() {
	case "q":
		return tea.Quit
	case "tab":
		a.focus = focusSidebar
	case "esc", "left", "h", "backspace":
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
		} else {
			a.focus = focusSidebar
		}
	case "up", "k":
		a.moveCursor(lvl, -1)
	case "down", "j":
		a.moveCursor(lvl, 1)
	case "pgup", "ctrl+u":
		a.moveCursor(lvl, -a.listHeight())
	case "pgdown", "ctrl+d":
		a.moveCursor(lvl, a.listHeight())
	case "g", "home":
		a.moveCursor(lvl, -len(lvl.keys))
	case "G", "end":
		a.moveCursor(lvl, len(lvl.keys))
	case "enter", "right", "l":
		if lvl.cursor < len(lvl.keys) {
			return a.openSheet(lvl, lvl.keys[lvl.cursor])
		}
	case "r":
		return a.reload(lvl)
	case "/":
		if !lvl.fixed {
			return a.startInput(inputFilter, lvl.filter, "pk glob, or index=value")
		}
	case "o":
		return a.startInput(inputOptions, formatOptions(a.options), "placeholder=value, …")
	}

	if !lvl.done && lvl.cursor >= len(lvl.keys)-a.listHeight()/2 {
		return a.loadPage(lvl)
	}

	return nil
}

func (a *app) moveCursor(lvl *level, delta int) {
	lvl.cursor = max(0, min(lvl.cursor+delta, len(lvl.keys)-1))
}

func (a *app) startInput(mode inputMode, value, placeholder string) tea.Cmd {
	a.mode = mode
	a.input.Placeholder = placeholder
	a.input.SetValue(value)
	a.input.CursorEnd()

	return a.input.Focus()
}

func (a *app) updateInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.mode = inputNone
		a.input.Blur()
		return nil

	case "enter":
		mode := a.mode
		a.mode = inputNone
		a.input.Blur()

		lvl := a.current()
		if lvl == nil {
			return nil
		}

		if mode == inputFilter {
			lvl.filter = strings.TrimSpace(a.input.Value())
		} else {
			a.options = parseOptions(a.input.Value())
		}

		return a.reload(lvl)
	}

	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	return cmd
}

func (a *app) updateSheet(msg tea.KeyPressMsg) tea.Cmd {
	sh := a.sheet

	switch msg.String() {
	case "esc", "q":
		a.sheet = nil
		return nil
	case "tab", "right", "l":
		if len(sh.tabs) > 0 {
			sh.active = (sh.active + 1) % len(sh.tabs)
			a.renderTab()
		}
		return nil
	case "shift+tab", "left", "h":
		if len(sh.tabs) > 0 {
			sh.active = (sh.active - 1 + len(sh.tabs)) % len(sh.tabs)
			a.renderTab()
		}
		return nil
	case "enter":
		// Follow a relation: navigate to its model, on its key.
		if sh.active >= len(sh.tabs) || !sh.tabs[sh.active].relation {
			return nil
		}

		t := sh.tabs[sh.active]
		if t.err != nil {
			return nil
		}

		lvl := &level{
			title:   t.name,
			storage: t.storage,
			keys:    []string{t.key},
			fixed:   true,
			done:    true,
		}
		a.stack = append(a.stack, lvl)
		a.focus = focusMain

		return a.openSheet(lvl, t.key)
	}

	var cmd tea.Cmd
	sh.vp, cmd = sh.vp.Update(msg)
	return cmd
}

// |------------------------
// | Layout
// |------------------------

const sidebarWidth = 30

func (a *app) bodyHeight() int {
	// Header, divider, footer.
	return max(a.height-3, 1)
}

func (a *app) listHeight() int {
	// Breadcrumb, info line and blank line.
	return max(a.bodyHeight()-3, 1)
}

func (a *app) sheetSize() (int, int) {
	return max(min(a.width-6, 110), 20), max(a.height-4, 6)
}

func (a *app) resizeSheet() {
	if a.sheet == nil {
		return
	}

	w, h := a.sheetSize()
	// Border, padding, title, meta and tab lines.
	a.sheet.vp.SetWidth(w - 4)
	a.sheet.vp.SetHeight(max(h-6, 1))
}

func (a *app) renderTab() {
	sh := a.sheet
	if sh == nil || sh.active >= len(sh.tabs) {
		return
	}

	t := sh.tabs[sh.active]
	switch {
	case t.err != nil:
		sh.vp.SetContent(a.st.err.Render(t.err.Error()))
	default:
		sh.vp.SetContent(highlightJSON(t.json, a.st))
	}
	sh.vp.GotoTop()
}

func (a *app) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.WindowTitle = "kvlens"

	if a.width == 0 {
		return v
	}

	header := a.viewHeader()
	divider := a.st.divider.Render(strings.Repeat("─", a.width))
	body := lipgloss.JoinHorizontal(lipgloss.Top, a.viewSidebar(), a.viewSeparator(), a.viewMain())
	footer := a.viewFooter()

	screen := lipgloss.JoinVertical(lipgloss.Left, header, divider, body, footer)

	if a.sheet != nil {
		modal := a.viewSheet()
		x := max((a.width-lipgloss.Width(modal))/2, 0)
		y := max((a.height-lipgloss.Height(modal))/2, 0)

		screen = lipgloss.NewCompositor(
			lipgloss.NewLayer(a.st.muted.Render(ansi.Strip(screen))),
			lipgloss.NewLayer(modal).X(x).Y(y).Z(1),
		).Render()
	}

	v.SetContent(screen)
	return v
}

func (a *app) viewHeader() string {
	left := a.st.badge.Render("kvlens") + " " + a.st.muted.Render(a.url)
	right := a.st.muted.Render(fmt.Sprintf("%d models", len(a.models)))

	gap := max(a.width-lipgloss.Width(left)-lipgloss.Width(right)-1, 1)
	return left + strings.Repeat(" ", gap) + right
}

func (a *app) viewSeparator() string {
	return a.st.divider.Render(strings.Repeat("│\n", a.bodyHeight()-1) + "│")
}

func (a *app) viewSidebar() string {
	w := sidebarWidth
	lines := []string{a.sectionTitle("Models", a.focus == focusSidebar), ""}

	if len(a.models) == 0 {
		lines = append(lines, a.st.muted.Render(" No models with a"), a.st.muted.Render(" storage option."))
	}

	// Two lines per model, scrolled to keep the selection visible.
	visible := max((a.bodyHeight()-2)/2, 1)
	start := max(0, a.selected-visible+1)

	for i := start; i < len(a.models) && i < start+visible; i++ {
		s := a.models[i]
		name := truncate(shortName(s.GetName()), w-3)
		prefix := truncate(s.GetPrefix(), w-3)

		if i == a.selected {
			marker := a.st.selected.Render("▌")
			nameStyle := a.st.selected
			if a.focus != focusSidebar {
				marker, nameStyle = a.st.muted.Render("▌"), a.st.selectedDim
			}
			lines = append(lines, marker+" "+nameStyle.Render(name), "  "+a.st.muted.Render(prefix))
		} else {
			lines = append(lines, "  "+name, "  "+a.st.muted.Render(prefix))
		}
	}

	return block(lines, w, a.bodyHeight())
}

func (a *app) sectionTitle(title string, focused bool) string {
	if focused {
		return " " + a.st.accent.Bold(true).Render(title)
	}

	return " " + a.st.title.Render(title)
}

func (a *app) viewMain() string {
	w := max(a.width-sidebarWidth-1, 10)
	h := a.bodyHeight()
	lvl := a.current()

	if lvl == nil {
		return block([]string{"", a.st.muted.Render("  Load models with -descriptors or KVLENS_DESCRIPTORS.")}, w, h)
	}

	// Breadcrumb: the navigation stack.
	crumbs := make([]string, 0, len(a.stack))
	for i, l := range a.stack {
		if i == len(a.stack)-1 {
			crumbs = append(crumbs, a.focusStyle().Render(l.title))
		} else {
			crumbs = append(crumbs, a.st.muted.Render(l.title))
		}
	}
	breadcrumb := " " + strings.Join(crumbs, a.st.muted.Render(" › "))

	lines := []string{breadcrumb, " " + a.levelInfo(lvl, w-2), ""}

	switch {
	case lvl.err != nil:
		lines = append(lines, " "+a.st.err.Render(truncate(lvl.err.Error(), w-2)))
	case len(lvl.keys) == 0 && lvl.loading:
		lines = append(lines, " "+a.st.muted.Render("Scanning…"))
	case len(lvl.keys) == 0:
		lines = append(lines, " "+a.st.muted.Render("No keys match."))
	default:
		lines = append(lines, a.viewKeys(lvl, w)...)
	}

	return block(lines, w, h)
}

func (a *app) focusStyle() lipgloss.Style {
	if a.focus == focusMain {
		return a.st.accent.Bold(true)
	}

	return a.st.title
}

func (a *app) levelInfo(lvl *level, w int) string {
	parts := []string{}

	count := fmt.Sprintf("%d keys", len(lvl.keys))
	if len(lvl.keys) == 1 {
		count = "1 key"
	}
	if !lvl.done {
		count += "+"
	}
	parts = append(parts, count)

	if lvl.filter != "" {
		parts = append(parts, "filter "+lvl.filter)
	}
	if len(a.options) > 0 {
		parts = append(parts, formatOptions(a.options))
	}
	if !lvl.fixed {
		parts = append(parts, scanPattern(lvl.storage, a.options, lvl.filter))
	}

	return a.st.muted.Render(truncate(strings.Join(parts, "  ·  "), w))
}

func (a *app) viewKeys(lvl *level, w int) []string {
	h := a.listHeight()

	// Scroll to keep the cursor visible.
	if lvl.cursor < lvl.offset {
		lvl.offset = lvl.cursor
	}
	if lvl.cursor >= lvl.offset+h {
		lvl.offset = lvl.cursor - h + 1
	}

	lines := make([]string, 0, h)
	for i := lvl.offset; i < len(lvl.keys) && i < lvl.offset+h; i++ {
		key := truncate(lvl.keys[i], w-4)
		if i == lvl.cursor && a.focus == focusMain {
			lines = append(lines, a.st.selected.Render(" ▌ "+key))
		} else {
			lines = append(lines, "   "+key)
		}
	}

	if lvl.loading && len(lines) < h {
		lines = append(lines, "   "+a.st.muted.Render("Loading…"))
	}

	return lines
}

func (a *app) viewFooter() string {
	if a.mode != inputNone {
		label := "Filter"
		if a.mode == inputOptions {
			label = "Options"
		}

		a.input.SetWidth(max(a.width-len(label)-4, 10))
		return " " + a.st.inputLine.Render(label+" ›") + " " + a.input.View()
	}

	var bindings [][2]string
	switch {
	case a.sheet != nil:
		bindings = [][2]string{{"tab", "relation"}, {"↑↓", "scroll"}, {"enter", "go to relation"}, {"esc", "close"}}
	case a.focus == focusSidebar:
		bindings = [][2]string{{"↑↓", "model"}, {"enter", "browse keys"}, {"q", "quit"}}
	default:
		bindings = [][2]string{{"↑↓", "key"}, {"enter", "open"}, {"/", "filter"}, {"o", "options"}, {"r", "reload"}, {"esc", "back"}, {"q", "quit"}}
	}

	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		parts = append(parts, a.st.helpKey.Render(b[0])+" "+a.st.help.Render(b[1]))
	}

	return " " + truncate(strings.Join(parts, a.st.help.Render("  ·  ")), a.width-1)
}

func (a *app) viewSheet() string {
	sh := a.sheet
	w, h := a.sheetSize()
	inner := w - 4

	title := a.st.title.Render(truncate(sh.key, inner))

	var meta, tabs string
	switch {
	case sh.loading:
		meta = a.st.muted.Render("Loading…")
	case sh.active < len(sh.tabs):
		t := sh.tabs[sh.active]
		parts := []string{string(t.storage.GetName())}
		if t.relation {
			parts = append(parts, t.key)
		}
		if t.size > 0 {
			parts = append(parts, fmt.Sprintf("%d bytes", t.size))
		}
		meta = a.st.muted.Render(truncate(strings.Join(parts, "  ·  "), inner))

		rendered := make([]string, 0, len(sh.tabs))
		for i, t := range sh.tabs {
			name := t.name
			if t.err != nil {
				name += " !"
			}

			if i == sh.active {
				rendered = append(rendered, a.st.tabActive.Render(name))
			} else {
				rendered = append(rendered, a.st.tabInactive.Render(name))
			}
		}
		tabs = truncate(strings.Join(rendered, " "), inner)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, meta, tabs, "", sh.vp.View())

	return a.st.sheet.Width(w).Height(h).Render(content)
}

// block pads lines into a w×h rectangle.
func block(lines []string, w, h int) string {
	if len(lines) > h {
		lines = lines[:h]
	}

	out := make([]string, h)
	for i := range h {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], w, "…")
		}
		out[i] = line + strings.Repeat(" ", max(w-ansi.StringWidth(line), 0))
	}

	return strings.Join(out, "\n")
}

func truncate(s string, w int) string {
	return ansi.Truncate(s, max(w, 1), "…")
}
