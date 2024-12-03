package lmsys

import (
	"chatgpt-adapter/core/common/toolcall"
	"chatgpt-adapter/core/common/vars"
	"chatgpt-adapter/core/gin/inter"
	"chatgpt-adapter/core/gin/model"
	"chatgpt-adapter/core/gin/response"
	"chatgpt-adapter/core/logger"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"strings"
	"time"
)

const ginTokens = "__tokens__"

func waitMessage(chatResponse chan string, cancel func(str string) bool) (content string, err error) {

	for {
		message, ok := <-chatResponse
		if !ok {
			break
		}

		if strings.HasPrefix(message, "error: ") {
			return "", errors.New(strings.TrimPrefix(message, "error: "))
		}

		message = strings.TrimPrefix(message, "text: ")
		if len(message) > 0 {
			content += message
			if cancel != nil && cancel(content) {
				return content, nil
			}
		}
	}

	return content, nil
}

func waitResponse(ctx *gin.Context, matchers []inter.Matcher, chatResponse chan string, sse bool) (content string) {
	created := time.Now().Unix()
	logger.Info("waitResponse ...")
	tokens := ctx.GetInt(ginTokens)

	for {
		raw, ok := <-chatResponse
		if !ok {
			raw = response.ExecMatchers(matchers, "", true)
			if raw != "" && sse {
				response.SSEResponse(ctx, Model, raw, created)
			}
			content += raw
			break
		}

		if strings.HasPrefix(raw, "error: ") {
			err := strings.TrimPrefix(raw, "error: ")
			logger.Error(err)
			if response.NotSSEHeader(ctx) {
				logger.Error(err)
				response.Error(ctx, -1, err)
			}
			return
		}

		raw = strings.TrimPrefix(raw, "text: ")
		contentL := len(raw)
		if contentL <= 0 {
			continue
		}

		logger.Debug("----- raw -----")
		logger.Debug(raw)

		raw = response.ExecMatchers(matchers, raw, false)
		if len(raw) == 0 {
			continue
		}

		if sse && len(raw) > 0 {
			response.SSEResponse(ctx, Model, raw, created)
		}
		content += raw
	}

	if content == "" && response.NotSSEHeader(ctx) {
		return
	}

	ctx.Set(vars.GinCompletionUsage, response.CalcUsageTokens(content, tokens))
	if !sse {
		response.Response(ctx, Model, content)
	} else {
		response.SSEResponse(ctx, Model, "[DONE]", created)
	}
	return
}

func mergeMessages(ctx *gin.Context, completion model.Completion) (newMessages string, err error) {
	var (
		messages    = completion.Messages
		specialized = ctx.GetBool("specialized")
		isC         = response.IsClaude(ctx, "", completion.Model)
	)

	messageL := len(messages)
	if specialized && isC && messageL == 3 {
		newMessages = messages[0].GetString("content")
		return
	}

	var (
		pos      = 0
		contents []string
	)

	for {
		if pos > messageL-1 {
			break
		}

		message := messages[pos]
		role, end := response.ConvertRole(ctx, message.GetString("role"))
		contents = append(contents, role+message.GetString("content")+end)
		pos++
	}

	newMessages = strings.Join(contents, "")
	if strings.HasSuffix(newMessages, "<|end|>\n\n") {
		newMessages = newMessages[:len(newMessages)-9]
	}
	return
}

func echoMessages(ctx *gin.Context, completion model.Completion) {
	content := ""
	var (
		toolMessages = toolcall.ExtractToolMessages(&completion)
	)

	messages, err := mergeMessages(ctx, completion)
	if err != nil {
		logger.Error(err)
		response.Error(ctx, -1, err)
		return
	}

	content = messages
	if len(toolMessages) > 0 {
		content += "\n----------toolCallMessages----------\n"
		chunkBytes, _ := json.MarshalIndent(toolMessages, "", "  ")
		content += string(chunkBytes)
	}

	response.Echo(ctx, completion.Model, content, completion.Stream)
}
