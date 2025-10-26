package service

import (
	"context"
	"essay-platform/biz/application/dto/sts"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"essay-platform/biz/infra/mapper"
	"essay-platform/biz/infra/sdk/wechat"
	"essay-platform/biz/infra/util/log"
	"fmt"
	"strings"

	"github.com/google/wire"
	"github.com/samber/lo"
	"github.com/silenceper/wechat/v2/miniprogram/subscribe"
)

type IMessageService interface {
	SendWechatMessage(ctx context.Context, req *sts.SendWechatMessageReq) (*sts.SendWechatMessageResp, error)
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

	// 1. 根据用户ID获取用户信息
	user, err := s.UserMapper.FindOne(ctx, req.UserId)
	if err != nil {
		log.Error("SendWechatMessage: 查找用户失败, userId=%s, err=%v", req.UserId, err)
		return &sts.SendWechatMessageResp{
			Success:  false,
			ErrorMsg: lo.ToPtr("用户不存在"),
		}, nil
	}

	// 2. 获取用户的微信openid
	var openId string
	var appId string

	// 从用户的认证信息中找到微信openid
	for _, auth := range user.Auth {
		if auth.Type == consts.AuthTypeWechatOpenId {
			openId = auth.Value
			appId = auth.AppId
			break
		}
	}

	if openId == "" {
		log.Error("SendWechatMessage: 用户未绑定微信, userId=%s", req.UserId)
		return &sts.SendWechatMessageResp{
			Success:  false,
			ErrorMsg: lo.ToPtr("用户未绑定微信账号"),
		}, nil
	}

	// 3. 获取对应的小程序SDK
	miniProgram := s.MiniProgramMap[appId]
	if miniProgram == nil {
		log.Error("SendWechatMessage: 未找到对应的小程序配置, appId=%s, 可用的appId列表: %v", appId, s.getAvailableAppIds())
		return &sts.SendWechatMessageResp{
			Success:  false,
			ErrorMsg: lo.ToPtr("小程序配置不存在"),
		}, nil
	}

	log.Info("SendWechatMessage: 找到小程序配置, appId=%s, openId=%s", appId, openId)

	// 4. 构建订阅消息
	message := &subscribe.Message{
		ToUser:     openId,
		TemplateID: req.TemplateId,
		Data:       make(map[string]*subscribe.DataItem),
	}

	// 转换模板数据
	for key, value := range req.TemplateData {
		message.Data[key] = &subscribe.DataItem{
			Value: value,
		}
	}

	// 设置可选参数
	if req.Page != nil {
		message.Page = *req.Page
	}
	if req.MiniProgramState != nil {
		message.MiniprogramState = *req.MiniProgramState
	}
	if req.Lang != nil {
		message.Lang = *req.Lang
	}

	// 5. 发送订阅消息
	err = miniProgram.Send(ctx, message)
	if err != nil {
		log.Error("SendWechatMessage: 发送订阅消息失败, openId=%s, templateId=%s, err=%v", openId, req.TemplateId, err)

		// 解析微信错误码，提供更友好的错误信息
		errorMsg := s.parseWechatError(err.Error())

		return &sts.SendWechatMessageResp{
			Success:  false,
			ErrorMsg: lo.ToPtr(errorMsg),
		}, nil
	}

	log.Info("SendWechatMessage: 发送订阅消息成功, userId=%s, openId=%s, templateId=%s", req.UserId, openId, req.TemplateId)

	return &sts.SendWechatMessageResp{
		Success: true,
	}, nil
}

// getAvailableAppIds 获取可用的AppID列表，用于调试
func (s *MessageService) getAvailableAppIds() []string {
	var appIds []string
	for appId := range s.MiniProgramMap {
		appIds = append(appIds, appId)
	}
	return appIds
}

// parseWechatError 解析微信错误码，返回友好的错误信息
func (s *MessageService) parseWechatError(errStr string) string {
	if strings.Contains(errStr, "43101") {
		return "用户未订阅该消息模板，请引导用户在小程序中订阅消息"
	}
	if strings.Contains(errStr, "43104") {
		return "模板参数不正确，请检查模板参数是否与微信公众平台配置一致"
	}
	if strings.Contains(errStr, "47003") {
		return "模板参数值长度过长，请缩短参数内容"
	}
	if strings.Contains(errStr, "41030") {
		return "页面路径不正确，请检查page参数"
	}
	if strings.Contains(errStr, "40037") {
		return "模板ID不正确，请检查templateId参数"
	}
	if strings.Contains(errStr, "45009") {
		return "接口调用超过限额，请稍后再试"
	}

	// 默认返回原始错误信息
	return fmt.Sprintf("发送消息失败: %s", errStr)
}
