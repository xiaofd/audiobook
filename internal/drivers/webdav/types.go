package webdav

// WebDAV PROPFIND 响应类型定义（参照 OpenList 的 drivers/<name>/types.go 组织方式）。

// multistatus PROPFIND 多状态响应根节点。
type multistatus struct {
	XMLName   struct{}         `xml:"multistatus"`
	Responses []davResponseItem `xml:"response"`
}

// davResponseItem 单个资源的属性。
type davResponseItem struct {
	Href     string `xml:"href"`
	Propstat []struct {
		Status string `xml:"status"`
		Prop   struct {
			ResourceType struct {
				// Collection 非 nil 表示目录
				Collection *struct{} `xml:"collection"`
			} `xml:"resourcetype"`
			GetContentLength string `xml:"getcontentlength"`
			GetLastModified  string `xml:"getlastmodified"`
		} `xml:"prop"`
	} `xml:"propstat"`
}
