package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type WBUserCommunicationListInput struct {
	IsAnswered bool
	NMID       int64
	Take       int
	Skip       int
	Order      string
	DateFrom   *time.Time
	DateTo     *time.Time
}

type WBNewFeedbacksQuestionsDTO struct {
	HasNewQuestions bool `json:"hasNewQuestions"`
	HasNewFeedbacks bool `json:"hasNewFeedbacks"`
}

type WBUnansweredCountDTO struct {
	CountUnanswered      int `json:"countUnanswered"`
	CountUnansweredToday int `json:"countUnansweredToday"`
}

type WBProductDetailsDTO struct {
	IMTID           int64  `json:"imtId"`
	NMID            int64  `json:"nmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	SupplierName    string `json:"supplierName"`
	BrandName       string `json:"brandName"`
	Size            string `json:"size,omitempty"`
}

type WBCommunicationAnswerDTO struct {
	Text       string `json:"text"`
	State      string `json:"state,omitempty"`
	Editable   bool   `json:"editable"`
	CreateDate string `json:"createDate,omitempty"`
}

type WBQuestionDTO struct {
	ID             string                    `json:"id"`
	Text           string                    `json:"text"`
	CreatedDate    string                    `json:"createdDate"`
	State          string                    `json:"state"`
	Answer         *WBCommunicationAnswerDTO `json:"answer"`
	ProductDetails WBProductDetailsDTO       `json:"productDetails"`
	WasViewed      bool                      `json:"wasViewed"`
	IsWarned       bool                      `json:"isWarned"`
}

type WBFeedbackDTO struct {
	ID               string                    `json:"id"`
	Text             string                    `json:"text"`
	Pros             string                    `json:"pros"`
	Cons             string                    `json:"cons"`
	ProductValuation int                       `json:"productValuation"`
	CreatedDate      string                    `json:"createdDate"`
	Answer           *WBCommunicationAnswerDTO `json:"answer"`
	State            string                    `json:"state"`
	ProductDetails   WBProductDetailsDTO       `json:"productDetails"`
	WasViewed        bool                      `json:"wasViewed"`
	UserName         string                    `json:"userName"`
	OrderStatus      string                    `json:"orderStatus"`
	SubjectID        int64                     `json:"subjectId"`
	SubjectName      string                    `json:"subjectName"`
}

type WBQuestionsListDTO struct {
	CountUnanswered int             `json:"countUnanswered"`
	CountArchive    int             `json:"countArchive"`
	Questions       []WBQuestionDTO `json:"questions"`
}

type WBFeedbacksListDTO struct {
	CountUnanswered int             `json:"countUnanswered"`
	CountArchive    int             `json:"countArchive"`
	Feedbacks       []WBFeedbackDTO `json:"feedbacks"`
}

type wbEnvelope[T any] struct {
	Data             T               `json:"data"`
	Error            bool            `json:"error"`
	ErrorText        string          `json:"errorText"`
	AdditionalErrors json.RawMessage `json:"additionalErrors"`
}

func (e *wbEnvelope[T]) wbError() (bool, string) {
	return e.Error, e.ErrorText
}

func (c *Client) GetNewFeedbacksQuestions(ctx context.Context, token string) (WBNewFeedbacksQuestionsDTO, error) {
	var envelope wbEnvelope[WBNewFeedbacksQuestionsDTO]
	if err := c.getFeedbacksEnvelope(ctx, token, "/api/v1/new-feedbacks-questions", &envelope); err != nil {
		return WBNewFeedbacksQuestionsDTO{}, err
	}
	return envelope.Data, nil
}

func (c *Client) GetUnansweredQuestionsCount(ctx context.Context, token string) (WBUnansweredCountDTO, error) {
	var envelope wbEnvelope[WBUnansweredCountDTO]
	if err := c.getFeedbacksEnvelope(ctx, token, "/api/v1/questions/count-unanswered", &envelope); err != nil {
		return WBUnansweredCountDTO{}, err
	}
	return envelope.Data, nil
}

func (c *Client) GetUnansweredFeedbacksCount(ctx context.Context, token string) (WBUnansweredCountDTO, error) {
	var envelope wbEnvelope[WBUnansweredCountDTO]
	if err := c.getFeedbacksEnvelope(ctx, token, "/api/v1/feedbacks/count-unanswered", &envelope); err != nil {
		return WBUnansweredCountDTO{}, err
	}
	return envelope.Data, nil
}

func (c *Client) ListQuestions(ctx context.Context, token string, input WBUserCommunicationListInput) (WBQuestionsListDTO, error) {
	path := "/api/v1/questions?" + userCommunicationListQuery(input, 10000).Encode()
	var envelope wbEnvelope[WBQuestionsListDTO]
	if err := c.getFeedbacksEnvelope(ctx, token, path, &envelope); err != nil {
		return WBQuestionsListDTO{}, err
	}
	return envelope.Data, nil
}

func (c *Client) ListFeedbacks(ctx context.Context, token string, input WBUserCommunicationListInput) (WBFeedbacksListDTO, error) {
	path := "/api/v1/feedbacks?" + userCommunicationListQuery(input, 5000).Encode()
	var envelope wbEnvelope[WBFeedbacksListDTO]
	if err := c.getFeedbacksEnvelope(ctx, token, path, &envelope); err != nil {
		return WBFeedbacksListDTO{}, err
	}
	return envelope.Data, nil
}

func (c *Client) getFeedbacksEnvelope(ctx context.Context, token, path string, target any) error {
	_, body, err := c.doFeedbacksRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("unmarshal WB user communication response: %w", err)
	}
	if envelope, ok := target.(interface{ wbError() (bool, string) }); ok {
		if hasError, errorText := envelope.wbError(); hasError {
			if errorText != "" {
				return fmt.Errorf("WB user communication API error: %s", errorText)
			}
			return fmt.Errorf("WB user communication API error")
		}
	}
	return nil
}

func userCommunicationListQuery(input WBUserCommunicationListInput, maxTake int) url.Values {
	take := input.Take
	if take <= 0 {
		take = 100
	}
	if take > maxTake {
		take = maxTake
	}
	skip := input.Skip
	if skip < 0 {
		skip = 0
	}

	values := url.Values{}
	values.Set("isAnswered", strconv.FormatBool(input.IsAnswered))
	values.Set("take", strconv.Itoa(take))
	values.Set("skip", strconv.Itoa(skip))
	if input.NMID > 0 {
		values.Set("nmId", strconv.FormatInt(input.NMID, 10))
	}
	if input.Order == "dateAsc" || input.Order == "dateDesc" {
		values.Set("order", input.Order)
	}
	if input.DateFrom != nil {
		values.Set("dateFrom", strconv.FormatInt(input.DateFrom.Unix(), 10))
	}
	if input.DateTo != nil {
		values.Set("dateTo", strconv.FormatInt(input.DateTo.Unix(), 10))
	}
	return values
}
