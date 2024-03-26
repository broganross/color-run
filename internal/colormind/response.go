package colormind

type listModelResponse struct {
	Result []string `json:"result"`
}

type getPaletteResponse struct {
	Result Palette `json:"result"`
}
