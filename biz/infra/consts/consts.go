package consts

import (
	"errors"

	"github.com/zeromicro/go-zero/core/stores/mon"
	"google.golang.org/grpc/status"
)

var (
	ErrNotFound        = mon.ErrNotFound
	ErrInvalidObjectId = errors.New("invalid objectId")
)

var (
	ErrCannotDeleteObject     = status.Error(10000, "can not delete object")
	ErrNoSuchUser             = status.Error(10001, "no such user")
	ErrWrongWechatCode        = status.Error(10002, "wrong wechat code")
	ErrInvalidArgument        = status.Error(10003, "invalid argument")
	ErrWrongPassword          = status.Error(10004, "wrong password")
	ErrOpenIdNotFind          = status.Error(10005, "openId not find")
	ErrGetToken               = status.Error(10006, "get wx token failed")
	ErrUnionIdIsNil           = status.Error(10007, "unionId is nil")
	ErrUnionIdIsWrong         = status.Error(10008, "unionId is wrong")
	ErrOpenIdIsNil            = status.Error(10009, "openId is nil")
	ErrOpenIdIsWrong          = status.Error(10010, "openId is wrong")
	ErrWechatPayRequestFailed = status.Error(10011, "wechat pay request failed")
)

const (
	AuthTypeEmail           = "email"
	AuthTypePhone           = "phone"
	AuthTypeAccountPassword = "account-password" // 账号密码, 需要区分注册登录
	AuthTypeWechatOpenId    = "wechat-openid"    // wx.login + jscode2session流程获取OpenID
	AuthTypeWechatUnionId   = "wechat-unionid"   // unionId, 跨应用统一用户标识
	AuthTypeWechatPhone     = "wechat-phone"     // getPhoneNumber登录
	AuthTypeWebPhone        = "web-phone"        // web端手机号登录
)

const (
	VerifyCodeKeyPrefix = "verify:"
	OAuthUrl            = "https://api.weixin.qq.com/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code"
	WXAccessTokenUrl    = "https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s" // 获取access_token https://developers.weixin.qq.com/doc/offiaccount/Basic_Information/Get_access_token.html
	WXUserPhoneUrl      = "https://api.weixin.qq.com/wxa/business/getuserphonenumber?access_token=%s"
)
