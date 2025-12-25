package service

import (
	"bytes"
	"context"
	"essay-platform/biz/application/dto/sts"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"essay-platform/biz/infra/mapper"
	"essay-platform/biz/infra/util"
	"essay-platform/biz/infra/util/log"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/google/wire"
)

type IPayService interface {
	CreateOrder(ctx context.Context, req *sts.CreateOrderReq) (*sts.CreateOrderResp, error)
}

type PayService struct {
	Config     *config.Config
	UserMapper *mapper.UserMapper
	PayMapper  *mapper.PayMapper
}

var PaySet = wire.NewSet(
	wire.Struct(new(PayService), "*"),
	wire.Bind(new(IPayService), new(*PayService)),
)

func (s *PayService) buildAuthorizationHeader(method, urlPath, body string) (string, error) {
	payConfig := s.Config.WechatPayConfig

	nonce := uuid.New().String()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	signature, err := util.GenerateWechatPaySignature(method, urlPath, timestamp, nonce, body, payConfig.ApiclientKey)
	if err != nil {
		return "", err
	}

	authorization := fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`,
		payConfig.MchId,
		nonce,
		signature,
		timestamp,
		payConfig.ApiclientSerialNo,
	)

	return authorization, nil
}

func (s *PayService) CreateOrder(ctx context.Context, req *sts.CreateOrderReq) (*sts.CreateOrderResp, error) {
	user, err := s.UserMapper.FindOne(ctx, req.UserId)
	if err != nil {
		log.Error("CreateOrder: 查找用户失败, userId=%s, err=%v", req.UserId, err)
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

	payConfig := s.Config.WechatPayConfig
	pay, err := s.PayMapper.Insert(ctx, appId, user.ID.Hex(), req.Amount)
	if err != nil {
		log.Error("CreateOrder: 创建支付订单失败, err=%v", err)
		return nil, err
	}

	reqBody := map[string]any{
		"appid":        appId,
		"mchid":        payConfig.MchId,
		"description":  req.Description,
		"out_trade_no": pay.OrderId,
		"notify_url":   payConfig.NotifyURL,
		"amount": map[string]any{
			"total": req.Amount,
		},
		"payer": map[string]any{
			"openid": openId,
		},
	}

	bodyBytes, err := sonic.Marshal(reqBody)
	if err != nil {
		return nil, consts.ErrInvalidArgument
	}

	urlPath := "/v3/pay/transactions/jsapi"
	authorization, err := s.buildAuthorizationHeader(http.MethodPost, urlPath, string(bodyBytes)) // https://pay.weixin.qq.com/doc/v3/merchant/4012365336
	if err != nil {
		return nil, fmt.Errorf("生成签名失败: %w", err)
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": authorization,
	}

	respBytes, err := util.HTTPPostWithHeaders(ctx, payConfig.PayURL, headers, bytes.NewReader(bodyBytes))
	if err != nil {
		pay.Order.StatusMessage = string(respBytes)
		s.PayMapper.Update(ctx, pay)
		return nil, consts.ErrWechatPayRequestFailed
	}
	var respData map[string]any
	if err := sonic.Unmarshal(respBytes, &respData); err != nil {
		pay.Order.StatusMessage = string(respBytes)
		s.PayMapper.Update(ctx, pay)
		return nil, consts.ErrInvalidArgument
	}

	prepayId, _ := respData["prepay_id"].(string)

	return &sts.CreateOrderResp{
		OrderId:  pay.OrderId,
		PrepayId: prepayId,
	}, nil
}
