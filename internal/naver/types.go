package naver

import "time"

// Folder는 저장 리스트(폴더) 메타데이터. v3 shares API의 folder 객체.
type Folder struct {
	FolderID          int64             `json:"folderId"`
	ShareID           string            `json:"shareId"`
	Name              string            `json:"name"`
	Memo              string            `json:"memo"`
	ExternalLink      string            `json:"externalLink"`
	BookmarkCount     int               `json:"bookmarkCount"`
	FolderType        string            `json:"folderType"`
	LastUseTime       int64             `json:"lastUseTime"`
	CreationTime      int64             `json:"creationTime"`
	FollowCount       int               `json:"followCount"`
	ViewCount         int               `json:"viewCount"`
	PlaceUserProfile  *PlaceUserProfile `json:"placeUserProfile"`
	PublicationStatus string            `json:"publicationStatus"`
}

// PlaceUserProfile은 리스트 작성자 정보.
type PlaceUserProfile struct {
	PlaceUserID   string `json:"placeUserId"`
	Nick          string `json:"nick"`
	ReviewPageURL string `json:"reviewPageUrl"`
	ImageURL      string `json:"imageUrl"`
}

// Bookmark는 저장된 장소 하나. sid가 네이버 플레이스 ID로, 장소 upsert 키.
type Bookmark struct {
	BookmarkID     int64         `json:"bookmarkId"`
	SID            string        `json:"sid"`
	Name           string        `json:"name"`
	DisplayName    string        `json:"displayName"`
	Address        string        `json:"address"`
	Px             float64       `json:"px"` // 경도
	Py             float64       `json:"py"` // 위도
	Type           string        `json:"type"`
	MCID           string        `json:"mcid"`
	MCIDName       string        `json:"mcidName"`
	Memo           string        `json:"memo"`
	URL            string        `json:"url"`
	RCode          string        `json:"rcode"`
	CIDPath        []string      `json:"cidPath"`
	Available      bool          `json:"available"`
	IsIndoor       bool          `json:"isIndoor"`
	UseTime        int64         `json:"useTime"`
	LastUpdateTime int64         `json:"lastUpdateTime"`
	CreationTime   int64         `json:"creationTime"`
	MismatchInfo   *MismatchInfo `json:"bookmarkMismatchInfo"`
}

// MismatchInfo는 저장 당시 장소 정보와 현재 플레이스 정보의 일치 여부.
type MismatchInfo struct {
	IsMatched bool     `json:"isMatched"`
	Details   []string `json:"details"`
}

// ShareResponse는 v3 shares API의 top-level 응답.
type ShareResponse struct {
	Folder           Folder     `json:"folder"`
	BookmarkList     []Bookmark `json:"bookmarkList"`
	UnavailableCount int        `json:"unavailableCount"`
	MismatchedCount  int        `json:"mismatchedCount"`
}

// Title은 사용자가 이름을 바꿨으면 그 이름을, 아니면 원래 상호명을 돌려준다.
func (b Bookmark) Title() string {
	if b.DisplayName != "" {
		return b.DisplayName
	}
	return b.Name
}

// UpdatedAt은 lastUpdateTime(epoch ms)을 time.Time으로 변환한다.
func (b Bookmark) UpdatedAt() time.Time {
	return time.UnixMilli(b.LastUpdateTime)
}
