package service

import (
	"context"
	"encoding/json"
	"essay-platform/biz/application/dto/sts"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"essay-platform/biz/infra/mapper"
	"essay-platform/biz/infra/sdk/wechat"
	"essay-platform/biz/infra/util"
	"essay-platform/biz/infra/util/log"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/wire"
	"github.com/silenceper/wechat/v2/miniprogram/subscribe"
)

type IMessageService interface {
	SendWechatMessage(ctx context.Context, req *sts.SendWechatMessageReq) (*sts.SendWechatMessageResp, error)
	GenerateUrlLink(ctx context.Context, req *sts.GenerateUrlLinkReq) (*sts.GenerateUrlLinkResp, error)
}

type MessageService struct {
	Config         *config.Config
	UserMapper     *mapper.UserMapper
	MiniProgramMap wechat.MiniProgramMap
}

var MessageSet = wire.NewSet(
	wire.Struct(new(MessageService), "*"),
	wire.Bind(new(IMessageService), new(*MessageService)),
)

func (s *MessageService) SendWechatMessage(ctx context.Context, req *sts.SendWechatMessageReq) (*sts.SendWechatMessageResp, error) {
	log.Info("SendWechatMessage: 开始发送微信消息, userId=%s, templateId=%s", req.UserId, req.TemplateId)

	user, err := s.UserMapper.FindOne(ctx, req.UserId)
	if err != nil {
		return nil, consts.ErrNoSuchUser
	}

	var openId string
	var appId string

	for _, auth := range user.Auth {
		if auth.Type == consts.AuthTypeWechatOpenId {
			openId = auth.Value
			appId = auth.AppId
			break
		}
	}

	if openId == "" {
		return nil, consts.ErrOpenIdNotFind
	}

	miniProgram := s.MiniProgramMap[appId]
	if miniProgram == nil {
		return nil, consts.ErrNotFound
	}

	message := &subscribe.Message{
		ToUser:     openId,
		TemplateID: req.TemplateId,
		Data:       make(map[string]*subscribe.DataItem),
	}

	for key, value := range req.TemplateData {
		message.Data[key] = &subscribe.DataItem{
			Value: value,
		}
	}

	if req.Page != nil {
		message.Page = *req.Page
	}
	if req.MiniProgramState != nil {
		message.MiniprogramState = *req.MiniProgramState
	}
	if req.Lang != nil {
		message.Lang = *req.Lang
	}

	err = miniProgram.Send(ctx, message)
	if err != nil {
		return nil, consts.ErrWrongWechatCode
	}

	return nil, nil
}

func (s *MessageService) getAvailableAppIds() []string {
	var appIds []string
	for appId := range s.MiniProgramMap {
		appIds = append(appIds, appId)
	}
	return appIds
}

func (s *MessageService) GenerateUrlLink(ctx context.Context, req *sts.GenerateUrlLinkReq) (*sts.GenerateUrlLinkResp, error) {
	accessToken, err := s.getAccessToken(ctx, req.AppId)
	if err != nil {
		return nil, err
	}

	requestBody := map[string]any{
		"expire_type":     1,
		"expire_interval": 30,
	}

	if req.Path != nil {
		requestBody["path"] = *req.Path
	}
	if req.Query != nil {
		requestBody["query"] = *req.Query
	}
	if req.MiniProgramState != nil {
		requestBody["env_version"] = *req.MiniProgramState
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("https://api.weixin.qq.com/wxa/generate_urllink?access_token=%s", accessToken)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	var wechatResp map[string]interface{}
	if err := json.Unmarshal(respBody, &wechatResp); err != nil {
		return nil, err
	}
	if errCode, ok := wechatResp["errcode"].(float64); ok && errCode != 0 {
		errMsg := wechatResp["errmsg"].(string)
		return nil, fmt.Errorf("微信API错误: %s (错误码: %v)", errMsg, errCode)
	}

	urlLink, ok := wechatResp["url_link"].(string)
	if !ok {
		return nil, fmt.Errorf("响应格式错误")
	}

	return &sts.GenerateUrlLinkResp{
		UrlLink: urlLink,
	}, nil
}

func (s *MessageService) getAccessToken(ctx context.Context, appId string) (string, error) {
	var accessToken string
	for _, conf := range s.Config.WechatApplicationConfigs {
		if appId == conf.AppID {
			res, err := util.HTTPGet(ctx, fmt.Sprintf(consts.WXAccessTokenUrl, conf.AppID, conf.AppSecret))
			if err != nil {
				return "", err
			}
			tokenRes := make(map[string]any)
			if err = sonic.Unmarshal(res, &tokenRes); err != nil {
				return "", err
			}
			if accessToken = tokenRes["access_token"].(string); accessToken == "" {
				return "", consts.ErrGetToken
			}
			break
		}
	}
	return accessToken, nil
}
