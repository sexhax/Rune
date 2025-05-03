package api

import (
	"regexp"
	"strings"

	"github.com/valyala/fasthttp"
)

var (
	JS_FILE_REGEX    = regexp.MustCompile(`([a-zA-z0-9]+)\.js`)
	BUILD_INFO_REGEX = regexp.MustCompile(`Build Number: "\)\.concat\("([0-9]{4,8})"`)
)

func getLatestBuild() (string, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.Header.SetMethod(fasthttp.MethodGet)
	req.SetRequestURI("https://discord.com/app")

	if err := requestClient.Do(req, resp); err != nil {
		return "0", err
	}

	matches := JS_FILE_REGEX.FindAllString(string(resp.Body()), -1)
	if len(matches) == 0 {
		return "0", nil
	}
	
	asset := matches[len(matches)-1]

	if strings.Contains(asset, "invisible") {
		if len(matches) < 2 {
			return "0", nil
		}
		asset = matches[len(matches)-2]
	}

	req.Header.SetMethod(fasthttp.MethodGet)
	req.SetRequestURI("https://discord.com/assets/" + asset)

	if err := requestClient.Do(req, resp); err != nil {
		return "0", err
	}

	matches = BUILD_INFO_REGEX.FindAllString(string(resp.Body()), -1)
	if len(matches) == 0 {
		return "0", nil
	}
	
	match := strings.ReplaceAll(matches[0], " ", "")
	buildInfos := strings.Split(match, ",")
	buildNum := strings.Split(buildInfos[0], `("`)

	if len(buildNum) == 0 {
		return "0", nil
	}

	return strings.ReplaceAll(buildNum[len(buildNum)-1], `"`, ``), nil
}

func mustGetLatestBuild() string {
	if build, err := getLatestBuild(); err != nil {
		return "0"
	} else {
		return build
	}
}
