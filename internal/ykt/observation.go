package ykt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// RequestObservation 不包含路径参数、查询、请求头、响应体或原始错误。
type RequestObservation struct {
	At            time.Time
	Endpoint      string
	Status        int
	DurationMS    int64
	Outcome       string
	SessionCookie bool
	CSRFCookie    bool
}

type Observer interface {
	Request(RequestObservation)
	AuthResult(time.Time, string)
}

func observationOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrAuthExpired):
		return "auth_expired"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	default:
		return "upstream"
	}
}

func observationEndpoint(path string) string {
	switch {
	case path == pathUserBasicInfo:
		return "basic_info"
	case path == pathCourseList:
		return "courses"
	case path == pathChapter:
		return "chapters"
	case strings.HasPrefix(path, "/mooc-api/v1/lms/learn/leaf_info/"):
		return "leaf_info"
	case path == pathExamCover:
		return "exam_cover"
	case strings.HasPrefix(path, "/v2/api/web/logs/learn/"):
		return "learn_logs"
	case path == pathCourseMetaUserInfo:
		return "course_meta"
	default:
		return "other"
	}
}

func recordResponseCookies(event *RequestObservation, response *http.Response) {
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "sessionid":
			event.SessionCookie = true
		case "csrftoken":
			event.CSRFCookie = true
		}
	}
}
