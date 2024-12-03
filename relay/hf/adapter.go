package hf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"chatgpt-adapter/core/common"
	"chatgpt-adapter/core/common/agent"
	"chatgpt-adapter/core/gin/inter"
	"chatgpt-adapter/core/gin/model"
	"chatgpt-adapter/core/logger"
	"github.com/bincooo/emit.io"
	"github.com/gin-gonic/gin"
	"github.com/iocgo/sdk/env"
)

var (
	ginSpace = "__prodia_space__"
)

type api struct {
	inter.BaseAdapter

	env *env.Environment
}

func (api *api) Match(ctx *gin.Context, model string) (ok bool, err error) {
	if model != "dall-e-3" {
		return
	}

	token := ctx.GetString("token")
	if token == "sk-prodia-sd" {
		ctx.Set(ginSpace, "prodia-sd")
		ok = true
		return
	}

	if token == "sk-prodia-xl" {
		ctx.Set(ginSpace, "prodia-xl")
		ok = true
		return
	}

	if token == "sk-google-xl" {
		ctx.Set(ginSpace, "google")
		ok = true
		return
	}

	if token == "sk-dalle-4k" {
		ctx.Set(ginSpace, "dalle-4k")
		ok = true
		return
	}

	if token == "sk-dalle-3-xl" {
		ctx.Set(ginSpace, "dalle-3xl")
		ok = true
		return
	}

	if token == "sk-animagine-xl-3.1" {
		ctx.Set(ginSpace, "animagine-xl-3.1")
		ok = true
		return
	}

	return
}

func (api *api) Generation(ctx *gin.Context) (err error) {
	var (
		value        = ""
		modelSlice   []string
		samplesSlice []string
		space        = ctx.GetString(ginSpace)
		generation   = common.GetGinGeneration(ctx)
	)

	message, err := completeTagsGenerator(ctx, api.env, generation.Message)
	if err != nil {
		logger.Error(err)
		return
	}

	message = strings.TrimSpace(message)
	message = strings.ReplaceAll(message, "。", "")
	message = strings.ReplaceAll(message, ".", "")
	message = strings.ReplaceAll(message, "\n", "")
	mod := matchModel(generation.Style, space)
	samples := matchSamples(generation.Quality, space)

	logger.Infof("curr space info[%s]: %s, %s", space, mod, samples)
	switch space {
	case "prodia-xl":
		modelSlice = XL_MODELS
		samplesSlice = XL_SAMPLES
		value, err = Ox1(ctx, api.env, mod, samples, message)
	case "dalle-4k":
		modelSlice = DALLE4K_MODELS
		value, err = Ox2(ctx, api.env, mod, message)
	case "dalle-3xl":
		value, err = Ox3(ctx, api.env, message)
	case "animagine-xl-3.1":
		modelSlice = ANIMAGINE_XL31_MODELS
		samplesSlice = ANIMAGINE_XL31_SAMPLES
		value, err = Ox4(ctx, api.env, mod, samples, message)
	case "google":
		modelSlice = GOOGLE_MODELS
		value, err = google(ctx, api.env, mod, message)
	default:
		modelSlice = SD_MODELS
		samplesSlice = SD_SAMPLES
		value, err = Ox0(ctx, api.env, mod, samples, message)
	}

	if err != nil {
		logger.Error(err)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"created": time.Now().Unix(),
		"styles":  modelSlice,
		"samples": samplesSlice,
		"data": []map[string]string{
			{"url": value},
		},
		"prompt":      message + ", {{{{by famous artist}}}, beautiful, masterpiece, 4k",
		"currStyle":   mod,
		"currSamples": samples,
	})
	return
}

func matchSamples(samples, spase string) string {
	switch spase {
	case "prodia-xl":
		if slices.Contains(XL_SAMPLES, samples) {
			return samples
		}
		return "Euler a"
	case "dalle-3xl":
		return "none"
	case "animagine-xl-3.1":
		if slices.Contains(ANIMAGINE_XL31_SAMPLES, samples) {
			return samples
		}
		return "Euler a"
	default:
		if slices.Contains(SD_SAMPLES, samples) {
			return samples
		}
		return "Euler a"
	}
}

func matchModel(style, spase string) string {
	switch spase {
	case "prodia-xl":
		if slices.Contains(XL_MODELS, style) {
			return style
		}
		return XL_MODELS[rand.Intn(len(XL_MODELS))]

	case "dalle-4k":
		if slices.Contains(DALLE4K_MODELS, style) {
			return style
		}
		return DALLE4K_MODELS[rand.Intn(len(DALLE4K_MODELS))]

	case "google":
		if slices.Contains(GOOGLE_MODELS, style) {
			return style
		}
		return GOOGLE_MODELS[rand.Intn(len(GOOGLE_MODELS))]

	case "dalle-3xl":
		return "none"

	case "animagine-xl-3.1":
		if slices.Contains(ANIMAGINE_XL31_MODELS, style) {
			return style
		}
		return ANIMAGINE_XL31_MODELS[rand.Intn(len(ANIMAGINE_XL31_MODELS))]

	default:
		if slices.Contains(SD_MODELS, style) {
			return style
		}
		return SD_MODELS[rand.Intn(len(SD_MODELS))]
	}
}

func completeTagsGenerator(ctx *gin.Context, env *env.Environment, content string) (string, error) {
	var (
		proxied = env.GetString("server.proxied")
		mod     = env.GetString("llm.model")
		cookie  = env.GetString("llm.token")
		baseUrl = env.GetString("llm.base-url")
	)

	c := regexp.MustCompile("<tag content=\"([^>]+)\"\\s?/>")
	matched := c.FindAllStringSubmatch(content, -1)
	var contents []string
	if len(matched) > 0 {
		for _, slice := range matched {
			content = strings.Replace(content, slice[0], "", -1)
			contents = append(contents, slice[1])
		}
	}

	content = strings.TrimSpace(content)
	if len(content) == 0 {
		return strings.Join(contents, ", "), nil
	}

	if strings.Contains(content, "<tag llm=false />") {
		contents = append(contents, strings.Replace(content, "<tag llm=false />", "", -1))
		return strings.Join(contents, ", "), nil
	}

	w := agent.SDWords
	if ctx.GetString(ginSpace) == "dalle-4k" || ctx.GetString(ginSpace) == "dalle-3xl" {
		w = agent.SD2Words
	}

	obj := map[string]interface{}{
		"model":  mod,
		"stream": false,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": strings.Replace(w, "{{content}}", content, -1),
			},
		},
		"temperature": .8,
		"max_tokens":  4096,
	}

	res, err := fetch(ctx.Request.Context(), proxied, baseUrl, cookie, obj)
	if err != nil {
		return "", err
	}

	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	var r model.Response
	if err = json.Unmarshal(data, &r); err != nil {
		logger.Error("data: %s", data)
		return "", err
	}

	if res.StatusCode != http.StatusOK {
		if r.Error != nil {
			return "", errors.New(r.Error.Message)
		} else {
			return "", errors.New(res.Status)
		}
	}

	message := strings.TrimSpace(r.Choices[0].Message.Content)
	left := strings.Index(message, `"""`)
	right := strings.LastIndex(message, `"""`)

	if left > -1 && left < right {
		message = strings.ReplaceAll(message[left+3:right], "\"", "")
		contents = append(contents, message)
		logger.Infof("system assistant generate message[%s]: %s", mod, strings.Join(contents, ", "))
		return strings.Join(contents, ", "), nil
	}

	if strings.HasSuffix(message, `"""`) { // 哎。bing 偶尔会漏掉前面的"""
		message = strings.ReplaceAll(message[:len(message)-3], "\"", "")
		contents = append(contents, message)
		logger.Infof("system assistant generate message[%s]: %s", mod, strings.Join(contents, ", "))
		return strings.Join(contents, ", "), nil
	}

	left = strings.Index(message, "```")
	right = strings.LastIndex(message, "```")

	if left > -1 && left < right {
		message = strings.ReplaceAll(message[left+3:right], "\"", "")
		contents = append(contents, message)
		logger.Infof("system assistant generate message[%s]: %s", mod, strings.Join(contents, ", "))
		return strings.Join(contents, ", "), nil
	}

	logger.Info("response content: ", message)
	logger.Errorf("system assistant generate message[%s] error: system assistant generate message failed", mod)
	return "", errors.New("system assistant generate message failed")
}

func fetch(ctx context.Context, proxied, baseUrl, cookie string, obj interface{}) (*http.Response, error) {
	if strings.Contains(baseUrl, "127.0.0.1") || strings.Contains(baseUrl, "localhost") {
		proxied = ""
	}

	return emit.ClientBuilder(common.HTTPClient).
		Context(ctx).
		Proxies(proxied).
		POST(fmt.Sprintf("%s/v1/chat/completions", baseUrl)).
		Header("Authorization", cookie).
		JHeader().
		Body(obj).
		DoC(emit.Status(http.StatusOK), emit.IsJSON)
}
