package types

import "time"

type TagListResponse struct {
	Results []TagResponse `json:"results"`
	Total   int64         `json:"total"`
}

type DeletedTagResponse struct {
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	TargetName string    `json:"target_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type DeletedTagListResponse struct {
	Results []DeletedTagResponse `json:"results"`
	Total   int64                `json:"total"`
}

type TagQualityCriteriaView struct {
	SubjectMatter      *bool `json:"subject_matter"`
	StandardTerm       *bool `json:"standard_term"`
	NotPersonPlaceWork *bool `json:"not_person_place_work"`
	AppropriatelyBroad *bool `json:"appropriately_broad"`
}

type TagQualityVerdictInfo struct {
	Pass      bool                    `json:"pass"`
	Criteria  *TagQualityCriteriaView `json:"criteria"`
	Model     string                  `json:"model,omitempty"`
	VerdictAt time.Time               `json:"verdict_at"`
}

type ProvisionalTagResponse struct {
	ID            int64                  `json:"id"`
	Name          string                 `json:"name"`
	DocumentCount int64                  `json:"document_count"`
	CreatedAt     time.Time              `json:"created_at"`
	Verdict       *TagQualityVerdictInfo `json:"verdict"`
}

type ProvisionalTagListResponse struct {
	Results []ProvisionalTagResponse `json:"results"`
	Total   int64                    `json:"total"`
}

type CreateTagRequest struct {
	Name string `json:"name"`
}

type UpdateTagRequest struct {
	Name string `json:"name"`
}

type MergeTagRequest struct {
	TargetID int64 `json:"target_id"`
}

type MergeTagResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Reassigned int64  `json:"reassigned"`
}

type MergePairRequest struct {
	TagA string `json:"tag_a"`
	TagB string `json:"tag_b"`
}
