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
		},
	}
}

// Presets is every preset by id.
func Presets() map[string]Preset {
	out := map[string]Preset{}
	for _, p := range []Preset{contentPreset()} {
		out[p.ID] = p
	}
	return out
}

// Apps are the apps over the engine, with the preset each starts a workspace from.
var Apps = map[string]string{"content": "content"}
