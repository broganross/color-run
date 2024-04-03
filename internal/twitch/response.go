package twitch

type ingestsResponse struct {
	Ingests []struct {
		ID           int     `json:"_id"`
		Availability float64 `json:"availability"`
		Default      bool    `json:"default"`
		Name         string  `json:"name"`
		URLTemplate  string  `json:"url_template"`
		Priority     int     `json:"priority"`
	} `json:"ingests"`
}
