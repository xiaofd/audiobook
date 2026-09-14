package mobilecloud

// 139 云盘 API 响应类型定义（参照 OpenList 的 drivers/<name>/types.go 组织方式）。

// personalListResp /file/list 响应。
type personalListResp struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []personalItem `json:"items"`
		// NextPageCursor 分页游标（空表示末页）
		NextPageCursor string `json:"nextPageCursor"`
	} `json:"data"`
}

// personalItem 单个文件/目录项。
type personalItem struct {
	FileId     string `json:"fileId"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Type       string `json:"type"` // "folder" 或 "file"
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// routeResp 路由策略查询响应。
type routeResp struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		RoutePolicyList []struct {
			ModName  string `json:"modName"`
			HttpsUrl string `json:"httpsUrl"`
		} `json:"routePolicyList"`
	} `json:"data"`
}

// downloadUrlResp /file/getDownloadUrl 响应。
type downloadUrlResp struct {
	Data struct {
		CDNUrl    string `json:"cdnUrl"`
		CDNSwitch bool   `json:"cdnSwitch"`
		URL       string `json:"url"`
	} `json:"data"`
}

// tokenRefreshResp 令牌刷新接口响应。
type tokenRefreshResp struct {
	Return string `json:"return"`
	Token  string `json:"token"`
}

// loginResp 登录接口响应（getLoginInfo / login 共用）。
type loginResp struct {
	Success bool `json:"success"`
	Data    struct {
		Authorization string `json:"authorization"`
		Account       string `json:"account"`
	} `json:"data"`
}
