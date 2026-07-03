package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

const communicationReputationSourceWB = "wb_user_communication_api"

// WBUserCommunicationReader reads real feedbacks/questions from the WB User Communication API.
type WBUserCommunicationReader interface {
	GetNewFeedbacksQuestions(ctx context.Context, token string) (wb.WBNewFeedbacksQuestionsDTO, error)
	GetUnansweredQuestionsCount(ctx context.Context, token string) (wb.WBUnansweredCountDTO, error)
	GetUnansweredFeedbacksCount(ctx context.Context, token string) (wb.WBUnansweredCountDTO, error)
	ListQuestions(ctx context.Context, token string, input wb.WBUserCommunicationListInput) (wb.WBQuestionsListDTO, error)
	ListFeedbacks(ctx context.Context, token string, input wb.WBUserCommunicationListInput) (wb.WBFeedbacksListDTO, error)
}

type SellerCabinetCommunicationReputation struct {
	SellerCabinetID uuid.UUID
	WBProductID     int64
	Source          string
	GeneratedAt     time.Time
	IsAnswered      bool
	NewItems        SellerCabinetCommunicationNewItems
	Counts          SellerCabinetCommunicationCounts
	Questions       []SellerCabinetQuestionEvidence
	Feedbacks       []SellerCabinetFeedbackEvidence
}

type SellerCabinetCommunicationNewItems struct {
	HasNewQuestions bool
	HasNewFeedbacks bool
}

type SellerCabinetCommunicationCounts struct {
	UnansweredQuestions      int
	UnansweredQuestionsToday int
	UnansweredFeedbacks      int
	UnansweredFeedbacksToday int
}

type SellerCabinetCommunicationProductDetails struct {
	IMTID           int64
	NMID            int64
	ProductName     string
	SupplierArticle string
	SupplierName    string
	BrandName       string
	Size            string
}

type SellerCabinetCommunicationAnswer struct {
	Text       string
	State      string
	Editable   bool
	CreateDate string
}

type SellerCabinetQuestionEvidence struct {
	ID             string
	Text           string
	CreatedDate    string
	State          string
	Answer         *SellerCabinetCommunicationAnswer
	ProductDetails SellerCabinetCommunicationProductDetails
	WasViewed      bool
	IsWarned       bool
}

type SellerCabinetFeedbackEvidence struct {
	ID               string
	Text             string
	Pros             string
	Cons             string
	ProductValuation int
	CreatedDate      string
	Answer           *SellerCabinetCommunicationAnswer
	State            string
	ProductDetails   SellerCabinetCommunicationProductDetails
	WasViewed        bool
	OrderStatus      string
	SubjectID        int64
	SubjectName      string
}

func (s *SellerCabinetService) GetCommunicationReputation(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, nmID int64, isAnswered bool, take int) (*SellerCabinetCommunicationReputation, error) {
	if nmID <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "nm_id is required")
	}
	if take <= 0 {
		take = 20
	}
	if take > 100 {
		take = 100
	}

	reader, ok := s.tokenValidator.(WBUserCommunicationReader)
	if !ok {
		return nil, apperror.New(apperror.ErrInternal, "WB User Communication API client is not configured")
	}

	cabinet, err := s.resolveCabinet(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return nil, err
	}

	wbToken, err := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to decrypt seller cabinet API token")
	}

	newItems, err := reader.GetNewFeedbacksQuestions(ctx, wbToken)
	if err != nil {
		return nil, wbUserCommunicationAPIError(err)
	}
	questionCounts, err := reader.GetUnansweredQuestionsCount(ctx, wbToken)
	if err != nil {
		return nil, wbUserCommunicationAPIError(err)
	}
	feedbackCounts, err := reader.GetUnansweredFeedbacksCount(ctx, wbToken)
	if err != nil {
		return nil, wbUserCommunicationAPIError(err)
	}

	input := wb.WBUserCommunicationListInput{
		IsAnswered: isAnswered,
		NMID:       nmID,
		Take:       take,
		Order:      "dateDesc",
	}
	questions, err := reader.ListQuestions(ctx, wbToken, input)
	if err != nil {
		return nil, wbUserCommunicationAPIError(err)
	}
	feedbacks, err := reader.ListFeedbacks(ctx, wbToken, input)
	if err != nil {
		return nil, wbUserCommunicationAPIError(err)
	}

	return &SellerCabinetCommunicationReputation{
		SellerCabinetID: cabinet.ID,
		WBProductID:     nmID,
		Source:          communicationReputationSourceWB,
		GeneratedAt:     time.Now().UTC(),
		IsAnswered:      isAnswered,
		NewItems: SellerCabinetCommunicationNewItems{
			HasNewQuestions: newItems.HasNewQuestions,
			HasNewFeedbacks: newItems.HasNewFeedbacks,
		},
		Counts: SellerCabinetCommunicationCounts{
			UnansweredQuestions:      questionCounts.CountUnanswered,
			UnansweredQuestionsToday: questionCounts.CountUnansweredToday,
			UnansweredFeedbacks:      feedbackCounts.CountUnanswered,
			UnansweredFeedbacksToday: feedbackCounts.CountUnansweredToday,
		},
		Questions: questionEvidenceFromWB(questions.Questions),
		Feedbacks: feedbackEvidenceFromWB(feedbacks.Feedbacks),
	}, nil
}

func wbUserCommunicationAPIError(err error) error {
	return apperror.New(apperror.ErrValidation, fmt.Sprintf("WB User Communication API request failed: %v", err))
}

func questionEvidenceFromWB(items []wb.WBQuestionDTO) []SellerCabinetQuestionEvidence {
	result := make([]SellerCabinetQuestionEvidence, 0, len(items))
	for _, item := range items {
		result = append(result, SellerCabinetQuestionEvidence{
			ID:             item.ID,
			Text:           item.Text,
			CreatedDate:    item.CreatedDate,
			State:          item.State,
			Answer:         answerEvidenceFromWB(item.Answer),
			ProductDetails: productDetailsFromWB(item.ProductDetails),
			WasViewed:      item.WasViewed,
			IsWarned:       item.IsWarned,
		})
	}
	return result
}

func feedbackEvidenceFromWB(items []wb.WBFeedbackDTO) []SellerCabinetFeedbackEvidence {
	result := make([]SellerCabinetFeedbackEvidence, 0, len(items))
	for _, item := range items {
		result = append(result, SellerCabinetFeedbackEvidence{
			ID:               item.ID,
			Text:             item.Text,
			Pros:             item.Pros,
			Cons:             item.Cons,
			ProductValuation: item.ProductValuation,
			CreatedDate:      item.CreatedDate,
			Answer:           answerEvidenceFromWB(item.Answer),
			State:            item.State,
			ProductDetails:   productDetailsFromWB(item.ProductDetails),
			WasViewed:        item.WasViewed,
			OrderStatus:      item.OrderStatus,
			SubjectID:        item.SubjectID,
			SubjectName:      item.SubjectName,
		})
	}
	return result
}

func productDetailsFromWB(item wb.WBProductDetailsDTO) SellerCabinetCommunicationProductDetails {
	return SellerCabinetCommunicationProductDetails{
		IMTID:           item.IMTID,
		NMID:            item.NMID,
		ProductName:     item.ProductName,
		SupplierArticle: item.SupplierArticle,
		SupplierName:    item.SupplierName,
		BrandName:       item.BrandName,
		Size:            item.Size,
	}
}

func answerEvidenceFromWB(item *wb.WBCommunicationAnswerDTO) *SellerCabinetCommunicationAnswer {
	if item == nil {
		return nil
	}
	return &SellerCabinetCommunicationAnswer{
		Text:       item.Text,
		State:      item.State,
		Editable:   item.Editable,
		CreateDate: item.CreateDate,
	}
}
