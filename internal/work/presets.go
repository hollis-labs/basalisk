package work

// Presets are the kind sets a workspace starts from. They are shipped here and copied into
// the workspace when it is made, so a workspace describes itself and its kinds can be
// edited there. An app (Content, Planner, Writer) picks its preset and its default views.

// Preset is a kind set and the views a new workspace gets.
type Preset struct {
	ID    string `json:"id"`
	App   string `json:"app"`
	Label string `json:"label"`
	Kinds []Kind `json:"kinds"`
	Views []View `json:"views"`
}

var contentStages = []string{"idea", "outline", "draft", "review", "scheduled", "published"}

func contentFields() []Field {
	return []Field{
		{Key: "publish_on", Label: "Publish on", Type: "date"},
		{Key: "channel", Label: "Channel", Type: "ref"},
		{Key: "format", Label: "Format", Type: "text"},
		{Key: "length", Label: "Length", Type: "text"},
	}
}

// contentPreset: ideas to published, for articles, videos, posts and games.
func contentPreset() Preset {
	return Preset{
		ID: "content", App: "content", Label: "Content",
		Kinds: []Kind{
			{Kind: "idea", Label: "Idea", Plural: "Ideas", Icon: "lightbulb", Color: "amber",
				Stages: contentStages, Fields: contentFields(), Sections: []string{"Pitch", "Notes"}},
			{Kind: "article", Label: "Article", Plural: "Articles", Icon: "file-text", Color: "blue",
				Stages: contentStages, Fields: contentFields(), Sections: []string{"Summary", "Outline", "Draft", "Notes"}},
			{Kind: "video", Label: "Video", Plural: "Videos", Icon: "clapperboard", Color: "red",
				Stages: contentStages, Fields: contentFields(), Sections: []string{"Summary", "Outline", "Script", "Notes"}},
			{Kind: "post", Label: "Post", Plural: "Posts", Icon: "message-square", Color: "teal",
				Stages: contentStages, Fields: contentFields(), Sections: []string{"Text", "Notes"}},
			{Kind: "game", Label: "Game", Plural: "Games", Icon: "gamepad-2", Color: "violet",
				Stages: contentStages, Fields: contentFields(), Sections: []string{"Pitch", "Design", "Notes"}},
			{Kind: "series", Label: "Series", Plural: "Series", Icon: "layers", Color: "slate",
				Stages: contentStages, Fields: contentFields(), Sections: []string{"Summary", "Notes"},
				Children: []string{"article", "video", "post", "game"}},
			{Kind: "channel", Label: "Channel", Plural: "Channels", Icon: "radio", Color: "gray",
				Stages: []string{}, Fields: []Field{{Key: "url", Label: "Address", Type: "url"}}, Sections: []string{"About", "Notes"}},
		},
		Views: []View{
			{View: "board", Title: "Board", Kinds: []string{"idea", "article", "video", "post", "game", "series"}, GroupBy: "stage"},
			{View: "calendar", Title: "Calendar", Kinds: []string{"idea", "article", "video", "post", "game", "series"}, DateFld: "publish_on"},
		},
	}
}

var manuscriptStages = []string{"idea", "outline", "draft", "revise", "final"}

// writerPreset: a book, a TTRPG setting or a campaign. The manuscript (parts, chapters,
// scenes, held in order) and the codex (characters, locations, items, factions, lore), with
// sessions and encounters for play. A ref field's options name the kinds it may point at.
func writerPreset() Preset {
	manuscript := []string{"part", "chapter", "scene", "session", "encounter"}
	codex := []string{"character", "location", "item", "faction", "lore"}
	return Preset{
		ID: "writer", App: "writer", Label: "Writer",
		Kinds: []Kind{
			{Kind: "part", Label: "Part", Plural: "Parts", Icon: "book", Color: "slate",
				Stages: manuscriptStages, Fields: []Field{}, Sections: []string{"Summary", "Notes"},
				Children: []string{"chapter"}},
			{Kind: "chapter", Label: "Chapter", Plural: "Chapters", Icon: "book-open", Color: "blue",
				Stages: manuscriptStages, Fields: []Field{}, Sections: []string{"Summary", "Notes"},
				Children: []string{"scene"}},
			{Kind: "scene", Label: "Scene", Plural: "Scenes", Icon: "pen-line", Color: "teal",
				Stages: manuscriptStages, Fields: []Field{
					{Key: "pov", Label: "Point of view", Type: "ref", Options: []string{"character"}},
					{Key: "location", Label: "Location", Type: "ref", Options: []string{"location"}},
					{Key: "when", Label: "When", Type: "text"},
				}, Sections: []string{"Summary", "Text", "Notes"}},
			{Kind: "character", Label: "Character", Plural: "Characters", Icon: "user", Color: "amber",
				Stages: []string{}, Fields: []Field{}, Sections: []string{"Role", "Description", "Voice", "Arc", "Notes"}},
			{Kind: "location", Label: "Location", Plural: "Locations", Icon: "map-pin", Color: "green",
				Stages: []string{}, Fields: []Field{
					{Key: "region", Label: "In", Type: "ref", Options: []string{"location"}},
				}, Sections: []string{"Description", "Notes"}},
			{Kind: "item", Label: "Item", Plural: "Items", Icon: "gem", Color: "violet",
				Stages: []string{}, Fields: []Field{
					{Key: "owner", Label: "Held by", Type: "ref", Options: []string{"character", "faction"}},
				}, Sections: []string{"Description", "Notes"}},
			{Kind: "faction", Label: "Faction", Plural: "Factions", Icon: "shield", Color: "red",
				Stages: []string{}, Fields: []Field{
					{Key: "members", Label: "Members", Type: "refs", Options: []string{"character"}},
					{Key: "base", Label: "Based in", Type: "ref", Options: []string{"location"}},
				}, Sections: []string{"Description", "Goals", "Notes"}},
			{Kind: "lore", Label: "Lore", Plural: "Lore", Icon: "scroll", Color: "gray",
				Stages: []string{}, Fields: []Field{}, Sections: []string{"Text", "Notes"}},
			{Kind: "session", Label: "Session", Plural: "Sessions", Icon: "calendar-days", Color: "orange",
				Stages: []string{"planned", "prepped", "played"}, Fields: []Field{
					{Key: "played_on", Label: "Played on", Type: "date"},
				}, Sections: []string{"Plan", "Recap", "Notes"}, Children: []string{"encounter", "scene"}},
			{Kind: "encounter", Label: "Encounter", Plural: "Encounters", Icon: "swords", Color: "rose",
				Stages: []string{}, Fields: []Field{
					{Key: "location", Label: "Location", Type: "ref", Options: []string{"location"}},
				}, Sections: []string{"Setup", "Notes"}},
		},
		Views: []View{
			{View: "outline", Title: "Outline", Kinds: manuscript},
			{View: "board", Title: "Board", Kinds: []string{"part", "chapter", "scene"}, GroupBy: "stage"},
			{View: "board", Title: "Codex", Kinds: codex, GroupBy: "kind"},
			{View: "canvas", Title: "Canvas", Kinds: append(append([]string{}, manuscript...), codex...)},
			{View: "flow", Title: "Plot", Kinds: []string{"chapter", "scene", "character", "location"}, Rel: []string{"leads-to", "appears-in"}},
		},
	}
}

// Presets is every preset by id.
func Presets() map[string]Preset {
	out := map[string]Preset{}
	for _, p := range []Preset{contentPreset(), writerPreset()} {
		out[p.ID] = p
	}
	return out
}
