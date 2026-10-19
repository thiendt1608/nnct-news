package brain

import "strings"

// ClusterSection decides the digest section of a cluster.
// Theo chủ đề của tin, không theo nguồn (tin OpenAI từ Hacker News hay GenK đều là "ai").
// Tin gắn chủ đề "vn-tech-startup" (về Việt Nam hoặc tác động trực tiếp tới người dùng/công ty VN) vào section "vn"
// để giữ hạn mức tin VN; báo VN đưa tin quốc tế thì không tính.
func ClusterSection(topics, sourceSections []string, bestSection string) string {
	for _, t := range topics {
		if t == "vn-tech-startup" {
			return "vn"
		}
	}
	if len(topics) > 0 {
		switch t := topics[0]; {
		case strings.HasPrefix(t, "ai-"), t == "agents", t == "research":
			return "ai"
		case t == "dev-tools", t == "backend", t == "cloud", t == "architecture",
			t == "system-design", t == "software-engineering", t == "algorithms":
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
