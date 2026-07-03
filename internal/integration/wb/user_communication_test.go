package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListFeedbacksUsesFeedbacksAPIAndRealQueryParams(t *testing.T) {
	dateFrom := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/feedbacks", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "false", r.URL.Query().Get("isAnswered"))
		assert.Equal(t, "5870243", r.URL.Query().Get("nmId"))
		assert.Equal(t, "50", r.URL.Query().Get("take"))
		assert.Equal(t, "10", r.URL.Query().Get("skip"))
		assert.Equal(t, "dateDesc", r.URL.Query().Get("order"))
		assert.Equal(t, "token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"countUnanswered":2,"countArchive":5,"feedbacks":[{"id":"feedback-1","text":"Спасибо, всё подошло","pros":"Удобный","cons":"Нет","productValuation":5,"createdDate":"2026-05-10T10:20:48+03:00","state":"wbRu","productDetails":{"nmId":5870243,"productName":"Товар","brandName":"Brand"},"wasViewed":true,"userName":"Покупатель","orderStatus":"buyout"}]},"error":false,"errorText":"","additionalErrors":null}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	got, err := client.ListFeedbacks(context.Background(), "token", WBUserCommunicationListInput{
		IsAnswered: false,
		NMID:       5870243,
		Take:       50,
		Skip:       10,
		Order:      "dateDesc",
		DateFrom:   &dateFrom,
		DateTo:     &dateTo,
	})

	require.NoError(t, err)
	require.Len(t, got.Feedbacks, 1)
	assert.Equal(t, "feedback-1", got.Feedbacks[0].ID)
	assert.Equal(t, "Спасибо, всё подошло", got.Feedbacks[0].Text)
	assert.Equal(t, int64(5870243), got.Feedbacks[0].ProductDetails.NMID)
}

func TestListQuestionsCapsTakeAtOfficialLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/questions", r.URL.Path)
		assert.Equal(t, "10000", r.URL.Query().Get("take"))
		assert.Equal(t, "0", r.URL.Query().Get("skip"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"countUnanswered":1,"countArchive":0,"questions":[{"id":"question-1","text":"Когда поставка?","createdDate":"2026-05-10T10:20:48Z","state":"wbRu","answer":null,"productDetails":{"nmId":224747484,"productName":"Карандаш"},"wasViewed":false,"isWarned":false}]},"error":false,"errorText":"","additionalErrors":null}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	got, err := client.ListQuestions(context.Background(), "token", WBUserCommunicationListInput{
		IsAnswered: false,
		Take:       50000,
		Skip:       -10,
	})

	require.NoError(t, err)
	require.Len(t, got.Questions, 1)
	assert.Equal(t, "question-1", got.Questions[0].ID)
	assert.Equal(t, "Когда поставка?", got.Questions[0].Text)
}

func TestUserCommunicationCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/new-feedbacks-questions":
			w.Write([]byte(`{"data":{"hasNewQuestions":true,"hasNewFeedbacks":false},"error":false}`))
		case "/api/v1/questions/count-unanswered":
			w.Write([]byte(`{"data":{"countUnanswered":24,"countUnansweredToday":3},"error":false}`))
		case "/api/v1/feedbacks/count-unanswered":
			w.Write([]byte(`{"data":{"countUnanswered":7,"countUnansweredToday":1},"error":false}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	newItems, err := client.GetNewFeedbacksQuestions(context.Background(), "token")
	require.NoError(t, err)
	assert.True(t, newItems.HasNewQuestions)
	assert.False(t, newItems.HasNewFeedbacks)

	questions, err := client.GetUnansweredQuestionsCount(context.Background(), "token")
	require.NoError(t, err)
	assert.Equal(t, 24, questions.CountUnanswered)
	assert.Equal(t, 3, questions.CountUnansweredToday)

	feedbacks, err := client.GetUnansweredFeedbacksCount(context.Background(), "token")
	require.NoError(t, err)
	assert.Equal(t, 7, feedbacks.CountUnanswered)
	assert.Equal(t, 1, feedbacks.CountUnansweredToday)
}
