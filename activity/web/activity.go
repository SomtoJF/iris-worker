package web

import "context"

type Activity struct{}

func NewActivity() *Activity {
	return &Activity{}
}

type ScrapeWebPageInput struct {
	Url string `json:"url"`
}

type ScrapeWebPageOutput struct {
	Data string `json:"data"`
}

func (a *Activity) ScrapeWebPage(ctx context.Context, input ScrapeWebPageInput) (ScrapeWebPageOutput, error) {
	return ScrapeWebPageOutput{}, nil
}
