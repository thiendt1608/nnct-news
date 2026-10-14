package brain

import "strings"

// ClusterSection decides the digest section of a cluster.
// Nguồn chỉ VN → "vn" (để giữ hạn mức tin VN); còn lại theo chủ đề chính của tin,
// không theo nguồn (tin OpenAI từ Hacker News vẫn là "ai").
func ClusterSection(topics, sourceSections []string, bestSection string) string {
	allVN := len(sourceSections) > 0
	for _, s := range sourceSections {
		if s != "vn" {
			allVN = false
		}
	}
	if allVN {
		return "vn"
	}
	if len(topics) > 0 {
		switch t := topics[0]; {
		case strings.HasPrefix(t, "ai-"):
			return "ai"
		case t == "dev-tools":
			return "dev"
		case t != "other":
			return "tech"
		}
	}
	if bestSection == "vn" {
		return "tech"
	}
	return bestSection
}
