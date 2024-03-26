package colormind

type getPaletteRequest struct {
	Model string   `json:"model"`
	Input *Palette `json:"input,omitempty"`
}
