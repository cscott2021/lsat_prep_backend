package learn

import "github.com/lsat-prep/backend/internal/models"

type Service struct {
	store *Store
}

func NewService(store *Store) *Service {
	return &Service{store: store}
}

func (s *Service) ListGuides(userID int64, section models.Section) (*models.LearnGuideListResponse, error) {
	items, err := s.store.ListGuides(userID, section)
	if err != nil {
		return nil, err
	}
	return &models.LearnGuideListResponse{Guides: items}, nil
}

func (s *Service) GetGuide(userID, guideID int64) (*models.LearnGuideDetailResponse, error) {
	guide, err := s.store.GetGuide(guideID)
	if err != nil {
		return nil, err
	}

	_ = s.store.RecordView(userID, guideID) // fire-and-forget; don't fail the request

	progress, _ := s.store.GetUserProgress(userID, guideID)
	viewed := progress != nil
	viewCount := 0
	if progress != nil {
		viewCount = progress.ViewCount
	}

	return &models.LearnGuideDetailResponse{
		Guide:     *guide,
		Viewed:    viewed,
		ViewCount: viewCount,
	}, nil
}

func (s *Service) GetGuideBySubtype(userID int64, section models.Section, subtype string) (*models.LearnGuideDetailResponse, error) {
	guide, err := s.store.GetGuideBySubtype(section, subtype)
	if err != nil {
		return nil, err
	}

	_ = s.store.RecordView(userID, guide.ID)

	progress, _ := s.store.GetUserProgress(userID, guide.ID)
	viewed := progress != nil
	viewCount := 0
	if progress != nil {
		viewCount = progress.ViewCount
	}

	return &models.LearnGuideDetailResponse{
		Guide:     *guide,
		Viewed:    viewed,
		ViewCount: viewCount,
	}, nil
}
