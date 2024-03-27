package colormind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"slices"
	"time"
)

var (
	ErrURLParse       = errors.New("parsing URL")
	ErrGet            = errors.New("executing get request")
	ErrResponseStatus = errors.New("invalid response status")
	ErrReadBody       = errors.New("reading response body")
	ErrParseBody      = errors.New("parsing response body")
	ErrPost           = errors.New("executing post request")
	ErrEmptyBody      = errors.New("response has empty body")
	ErrValidation     = errors.New("validation error")
	ErrEmptyPalette   = errors.New("palette may not be empty")

	emptyBytes = [...]byte{101, 109, 112, 116, 121, 32, 98, 111, 100, 121, 10}
)

type Color color.RGBA

func (c *Color) Unmarshal(v []byte) error {
	var values [3]uint8
	if err := json.Unmarshal(v, &values); err != nil {
		return err
	}
	c.R = values[0]
	c.G = values[1]
	c.B = values[2]
	c.A = 255
	return nil
}

func (c *Color) Marshal() ([]byte, error) {
	values := [3]uint8{c.R, c.G, c.B}
	return json.Marshal(values)
}

type Palette [5]*color.RGBA

func (p *Palette) UnmarshalJSON(b []byte) error {
	values := [5][3]uint8{}
	if err := json.Unmarshal(b, &values); err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		p[i] = &color.RGBA{
			values[i][0],
			values[i][1],
			values[i][2],
			255,
		}
	}
	return nil
}

func (p *Palette) MarshalJSON() ([]byte, error) {
	out := []byte("[")
	for i := 0; i < 5; i++ {
		// default: "N"
		b := []byte{34, 78, 34}
		c := p[i]
		if c != nil {
			colorValues := [3]uint8{c.R, c.G, c.B}
			bs, err := json.Marshal(&colorValues)
			if err != nil {
				return nil, fmt.Errorf("marshaling color: %w", err)
			}
			b = bs
		}
		out = append(out, b...)
		if i != 4 {
			out = append(out, []byte(",")...)
		}
	}
	out = append(out, []byte("]")...)
	return out, nil
}

type ColorMind struct {
	URL    string
	Client *http.Client
}

func New() *ColorMind {
	return &ColorMind{
		URL:    "http://colormind.io",
		Client: http.DefaultClient,
	}
}

func (c *ColorMind) GetPalette(model string, p *Palette) (*Palette, error) {
	return c.GetPaletteWithContext(context.Background(), model, p)
}

func (c *ColorMind) GetPaletteWithContext(ctx context.Context, model string, p *Palette) (*Palette, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// if given a palette it must contain at least one color
	if p != nil {
		cnt := 0
		for _, c := range p {
			if c != nil {
				cnt++
			}
		}
		if cnt == 0 {
			return nil, ErrEmptyPalette
		}
	}
	opts := &getPaletteRequest{
		Model: model,
		Input: p,
	}
	contents, err := json.Marshal(&opts)
	if err != nil {
		return nil, fmt.Errorf("marshaling request body: %w", err)
	}
	body := bytes.NewBuffer(contents)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/", c.URL), body)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPost, err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK {
		var contents string
		if resp.Body != nil {
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				// TODO: handle better
				return nil, fmt.Errorf("error reading response body: %w", err)
			}
			contents = string(b)
		}
		return nil, fmt.Errorf("%w (%s): %s", ErrResponseStatus, http.StatusText(resp.StatusCode), contents)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReadBody, err)
	}
	if slices.Compare(b, emptyBytes[:]) == 0 {
		return nil, ErrEmptyBody
	}
	respOpts := getPaletteResponse{}
	if err := json.Unmarshal(b, &respOpts); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseBody, err)
	}
	return &respOpts.Result, nil
}

func (c *ColorMind) ListModels() ([]string, error) {
	return c.ListModelsWithContext(context.Background())
}

func (c *ColorMind) ListModelsWithContext(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/list", c.URL), nil)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGet, err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	// TODO: Add more explicit status handling
	if resp.StatusCode != http.StatusOK {
		var contents string
		if resp.Body != nil {
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				// TODO: Handle this better
				return nil, fmt.Errorf("error reading response body: %w", err)
			}
			contents = string(b)
		}
		return nil, fmt.Errorf("%w (%s): %s", ErrResponseStatus, http.StatusText(resp.StatusCode), contents)
	}
	results := listModelResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseBody, err)
	}
	return results.Result, nil
}
