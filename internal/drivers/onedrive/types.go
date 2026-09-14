package onedrive

// Graph API 响应类型定义（参照 OpenList 的 drivers/<name>/types.go 组织方式）。

// graphChildrenResp 列目录响应（children 接口）。
type graphChildrenResp struct {
	Value    []graphItem `json:"value"`
	NextLink string      `json:"@odata.nextLink"` // 分页链接（默认每页最多 200 条）
}

// graphItem 单个文件/目录项。
type graphItem struct {
	Name        string      `json:"name"`
	Size        int64       `json:"size"`
	File        *struct{}   `json:"file"`
	Folder      *struct{}   `json:"folder"`
	LastMod     string      `json:"lastModifiedDateTime"`
	DownloadURL string      `json:"@microsoft.graph.downloadUrl"`
	Audio       *graphAudio `json:"audio"`
}

// graphAudio 音频元数据（Graph 自动解析）。
type graphAudio struct {
	Duration int64 `json:"duration"`
}

// graphTokenResp OAuth token 端点响应。
type graphTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// onlineTokenResp oplist.org 在线刷新 API 响应。
type onlineTokenResp struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	ErrorMessage string `json:"text"`
}
