package service

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"essay-platform/biz/application/dto/sts"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"essay-platform/biz/infra/data/db"
	"essay-platform/biz/infra/mapper"
	"essay-platform/biz/infra/sdk/wechat"
	"essay-platform/biz/infra/util"
	"essay-platform/biz/infra/util/log"
	"fmt"
	"math/big"
	"net/smtp"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/google/wire"
	"github.com/samber/lo"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tchttp "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/http"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"golang.org/x/crypto/bcrypt"
)

type IAuthenticationService interface {
	SignIn(ctx context.Context, req *sts.SignInReq) (*sts.SignInResp, error)
	SignUp(ctx context.Context, req *sts.SignUpReq) (*sts.SignUpResp, error)
	SetPassword(ctx context.Context, req *sts.SetPasswordReq) (*sts.SetPasswordResp, error)
	SendVerifyCode(ctx context.Context, req *sts.SendVerifyCodeReq) (*sts.SendVerifyCodeResp, error)
	AddAuth(ctx context.Context, req *sts.AddAuthReq) (*sts.AddAuthResp, error)
}

type AuthenticationService struct {
	Config         *config.Config
	UserMapper     *mapper.UserMapper
	MiniProgramMap wechat.MiniProgramMap
	Redis          *redis.Redis
}

var AuthenticationSet = wire.NewSet(
	wire.Struct(new(AuthenticationService), "*"),
	wire.Bind(new(IAuthenticationService), new(*AuthenticationService)),
)

func (s *AuthenticationService) AddAuth(ctx context.Context, req *sts.AddAuthReq) (*sts.AddAuthResp, error) {
	resp := &sts.AddAuthResp{
		UserId: req.UserId,
		AppId:  req.AuthId,
	}
	user, err := s.UserMapper.FindOne(ctx, req.UserId)
	switch err {
	case consts.ErrNotFound:
		return nil, consts.ErrNoSuchUser
	case nil:
		var err error
		switch req.AuthType {
		case consts.AuthTypeWechatPhone:
			resp.Options, err = s.BindWechatPhone(ctx, req, user)
		case consts.AuthTypeWechatOpenId:
			resp.Options, err = s.BindWechatOpenId(ctx, req, user)
		default:
			return nil, consts.ErrInvalidArgument
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return resp, nil
}

func (s *AuthenticationService) SignIn(ctx context.Context, req *sts.SignInReq) (*sts.SignInResp, error) {
	resp := &sts.SignInResp{}
	var err error
	switch req.AuthType {
	case consts.AuthTypeEmail:
		fallthrough
	case consts.AuthTypePhone:
		resp.UserId, err = s.signInByPassword(ctx, req)
	case consts.AuthTypeWechatOpenId:
		fallthrough
	case consts.AuthTypeWechatUnionId:
		resp.UserId, resp.UnionId, resp.OpenId, resp.AppId, err = s.signInByWechat(ctx, req)
	case consts.AuthTypeWechatPhone:
		resp.UserId, resp.Options, resp.AppId, err = s.SignInByWechatPhone(ctx, req) // 通过code获得的phone存在openId字段
	case consts.AuthTypeWebPhone:
		resp.UserId, resp.Options, resp.AppId, err = s.SignInByWebPhone(ctx, req)
	case consts.AuthTypeAccountPassword:
		resp.UserId, err = s.SignInByAccountPassword(ctx, req)
	default:
		return nil, consts.ErrInvalidArgument
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *AuthenticationService) SignUp(ctx context.Context, req *sts.SignUpReq) (*sts.SignUpResp, error) {
	resp := &sts.SignUpResp{}
	var err error
	switch req.AuthType {
	case consts.AuthTypeAccountPassword:
		resp.UserId, resp.Exist, err = s.SignUpByAccountPassword(ctx, req)
	default:
		return nil, consts.ErrInvalidArgument
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *AuthenticationService) signInByPassword(ctx context.Context, req *sts.SignInReq) (string, error) {
	UserMapper := s.UserMapper

	// 检查是否设置了验证码，若设置了检查验证码是否合法
	ok, err := s.checkVerifyCode(ctx, req.GetVerifyCode(), req.AuthId)
	if err != nil {
		return "", err
	}

	auth := &db.Auth{
		Type:  req.AuthType,
		Value: req.AuthId,
	}
	user, err := UserMapper.FindOneByAuth(ctx, auth)

	switch err {
	case nil:
	case consts.ErrNotFound:
		if !ok {
			return "", consts.ErrNoSuchUser
		}

		// 注册流程
		user = &db.User{Auth: []*db.Auth{auth}}
		err := UserMapper.Insert(ctx, user)
		if err != nil {
			return "", err
		}
		return user.ID.Hex(), nil
	default:
		return "", err
	}

	if ok {
		return user.ID.Hex(), nil
	}

	// 验证码未通过，尝试密码登录
	if user.Password == "" || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.GetPassword())) != nil {
		return "", consts.ErrWrongPassword
	}

	return user.ID.Hex(), nil
}

func (s *AuthenticationService) checkVerifyCode(ctx context.Context, except string, authValue string) (bool, error) {
	verifyCode, err := s.Redis.GetCtx(ctx, consts.VerifyCodeKeyPrefix+authValue)
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	} else if verifyCode == "" {
		logx.Info("查询到验证码为空")
		return false, nil
	} else if verifyCode != except {
		return false, nil
	} else {
		return true, nil
	}
}

func (s *AuthenticationService) SignInByWechatPhone(ctx context.Context, req *sts.SignInReq) (string, *string, string, error) {
	var appId string
	code := req.GetVerifyCode() // 微信接口提供的换取手机号的code
	var accessToken string
	for _, conf := range s.Config.WechatApplicationConfigs {
		if req.AuthId == conf.AppID {
			appId = conf.AppID
			res, err := util.HTTPGet(ctx, fmt.Sprintf(consts.WXAccessTokenUrl, conf.AppID, conf.AppSecret))
			logx.Info("微信AccessToken接口响应" + string(res))
			if err != nil {
				return "", nil, appId, err
			}
			tokenRes := make(map[string]any)
			if err = sonic.Unmarshal(res, &tokenRes); err != nil {
				return "", nil, appId, err
			}
			if accessToken = tokenRes["access_token"].(string); accessToken == "" {
				return "", nil, appId, consts.ErrGetToken
			}
			break
		}
	}

	bodyString := fmt.Sprintf(`{"code":"%s"}`, code)
	body := strings.NewReader(bodyString)
	res, err := util.HTTPPost(ctx, fmt.Sprintf(consts.WXUserPhoneUrl, accessToken), body)
	if err != nil {
		return "", nil, appId, err
	}

	var phoneRes map[string]any
	if err = sonic.Unmarshal(res, &phoneRes); err != nil {
		return "", nil, appId, err
	} else if phoneRes["errcode"].(float64) != 0 {
		return "", nil, appId, errors.New(phoneRes["errmsg"].(string))
	}
	phoneInfo, ok := phoneRes["phone_info"].(map[string]any)
	if !ok {
		return "", nil, appId, errors.New("phone_info 类型断言失败")
	}
	// 获取到的手机号，国外的会有区号
	phone := phoneInfo["phoneNumber"].(string)

	// 这里类型用"phone", 因为本质上还是有手机登录，只不过换了一种验证方式
	UserMapper := s.UserMapper
	auth := &db.Auth{
		Type:  consts.AuthTypePhone,
		Value: phone,
	}

	user, err := UserMapper.FindOneByAuth(ctx, auth)
	switch {
	case err == nil:
		// 找到了则直接返回id即可
		return user.ID.Hex(), &phone, appId, nil
	case errors.Is(err, consts.ErrNotFound):
		// 没找到需要创建
		user = &db.User{Auth: []*db.Auth{auth}}
		err = UserMapper.Insert(ctx, user)
		if err != nil {
			return "", &phone, appId, err
		}
		return user.ID.Hex(), &phone, appId, nil
	default:
		return "", &phone, appId, err
	}
}

func (s *AuthenticationService) BindWechatPhone(ctx context.Context, req *sts.AddAuthReq, user *db.User) (*string, error) {
	code := req.GetVerifyCode()
	var accessToken string
	for _, conf := range s.Config.WechatApplicationConfigs {
		if req.AuthId == conf.AppID {
			res, err := util.HTTPGet(ctx, fmt.Sprintf(consts.WXAccessTokenUrl, conf.AppID, conf.AppSecret))
			if err != nil {
				return nil, err
			}
			tokenRes := make(map[string]any)
			if err = sonic.Unmarshal(res, &tokenRes); err != nil {
				return nil, err
			}
			if accessToken = tokenRes["access_token"].(string); accessToken == "" {
				return nil, consts.ErrGetToken
			}
			break
		}
	}

	bodyString := fmt.Sprintf(`{"code":"%s"}`, code)
	body := strings.NewReader(bodyString)
	res, err := util.HTTPPost(ctx, fmt.Sprintf(consts.WXUserPhoneUrl, accessToken), body)
	if err != nil {
		log.Error("BindWechatPhone err:%+v", err)
		return nil, err
	}

	var phoneRes map[string]any
	if err = sonic.Unmarshal(res, &phoneRes); err != nil {
		return nil, err
	} else if phoneRes["errcode"].(float64) != 0 {
		log.Error("BindWechatPhone err:%+v", phoneRes)
		return nil, errors.New(phoneRes["errmsg"].(string))
	}
	phoneInfo, ok := phoneRes["phone_info"].(map[string]any)
	if !ok {
		return nil, errors.New("phone_info 类型断言失败")
	}
	phone := phoneInfo["phoneNumber"].(string)

	// 判断是否已经绑定过手机号，如果绑定过则更新手机号，未绑定过则添加手机号
	auth := &db.Auth{
		Type:  consts.AuthTypePhone,
		Value: phone,
	}
	_, find := lo.Find(user.Auth, func(item *db.Auth) bool {
		return *item == *auth
	})
	if find {
		return &phone, nil
	}
	_, find = lo.Find(user.Auth, func(item *db.Auth) bool {
		if item.Type == consts.AuthTypeWechatPhone {
			item.Value = phone
			return true
		}
		return false
	})
	if !find {
		user.Auth = append(user.Auth, auth)
	}
	err = s.UserMapper.Update(ctx, user)
	if err != nil {
		return nil, err
	}
	return &phone, nil
}

func (s *AuthenticationService) SignInByWebPhone(ctx context.Context, req *sts.SignInReq) (string, *string, string, error) {
	phone := req.AuthId
	appId := "Web-PiGaiBang"

	UserMapper := s.UserMapper
	auth := &db.Auth{
		Type:  consts.AuthTypePhone,
		Value: phone,
	}

	user, err := UserMapper.FindOneByAuth(ctx, auth)
	switch {
	case err == nil:
		return user.ID.Hex(), &phone, appId, nil
	case errors.Is(err, consts.ErrNotFound):
		user = &db.User{Auth: []*db.Auth{auth}}
		err = UserMapper.Insert(ctx, user)
		if err != nil {
			return "", &phone, appId, err
		}
		return user.ID.Hex(), &phone, appId, nil
	default:
		return "", &phone, appId, err
	}
}

func (s *AuthenticationService) SignInByAccountPassword(ctx context.Context, req *sts.SignInReq) (string, error) {
	auth := &db.Auth{
		Type:  req.AuthType,
		Value: req.AuthId,
	}
	user, err := s.UserMapper.FindOneByAuth(ctx, auth)

	switch err {
	case nil:
		// 登录
		if user.Password == "" || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.GetPassword())) != nil {
			return "", consts.ErrWrongPassword
		}
		return user.ID.Hex(), nil
	case consts.ErrNotFound:
		// 应走注册接口
		return "", consts.ErrNoSuchUser
	default:
		return "", err
	}
}

func (s *AuthenticationService) SignUpByAccountPassword(ctx context.Context, req *sts.SignUpReq) (string, bool, error) {
	auth := &db.Auth{
		Type:  req.AuthType,
		Value: req.AuthId,
	}
	user, err := s.UserMapper.FindOneByAuth(ctx, auth)

	switch err {
	case nil:
		// 返回已注册ID
		return user.ID.Hex(), true, nil
	case consts.ErrNotFound:
		user = &db.User{Auth: []*db.Auth{auth}, Password: req.GetPassword()}
		err := s.UserMapper.Insert(ctx, user)
		if err != nil {
			return "", false, err
		}
		return user.ID.Hex(), false, nil
	default:
		return "", false, err
	}
}

// signInByWechat 小程序登录https://developers.weixin.qq.com/miniprogram/dev/OpenApiDoc/user-login/code2Session.html#%E8%B0%83%E7%94%A8%E7%A4%BA%E4%BE%8B
func (s *AuthenticationService) signInByWechat(ctx context.Context, req *sts.SignInReq) (string, string, string, string, error) {
	jsCode := req.GetVerifyCode()

	var unionId string
	var openId string
	var appId string

	m := s.MiniProgramMap[req.GetAuthId()]
	if m != nil {
		// 向微信开放接口提交临时code
		res, err := m.Code2Session(ctx, jsCode)
		if err != nil {
			log.Error("Code2Session err:%+v", err)
			return "", "", "", "", err
		} else if res.ErrCode != 0 {
			return "", "", "", "", errors.New(res.ErrMsg)
		}
		unionId = res.UnionID
		openId = res.OpenID
		appId = req.GetAuthId()
	} else {
		for _, conf := range s.Config.WechatApplicationConfigs {
			if req.AuthId == conf.AppID {
				res, err := util.HTTPGet(ctx, fmt.Sprintf(consts.OAuthUrl, conf.AppID, conf.AppSecret, jsCode))
				if err != nil {
					return "", "", "", "", err
				}
				var j map[string]any
				if err = sonic.Unmarshal(res, &j); err != nil {
					return "", "", "", "", err
				}
				// unionid
				if unionidVal, exists := j["unionid"]; !exists || unionidVal == nil {
					return "", "", "", "", consts.ErrUnionIdIsNil
				} else if unionidStr, ok := unionidVal.(string); !ok || unionidStr == "" {
					return "", "", "", "", consts.ErrUnionIdIsWrong
				} else {
					unionId = unionidStr
				}

				// openid
				if openidVal, exists := j["openid"]; !exists || openidVal == nil {
					return "", "", "", "", consts.ErrOpenIdIsNil
				} else if openidStr, ok := openidVal.(string); !ok || openidStr == "" {
					return "", "", "", "", consts.ErrOpenIdIsWrong
				} else {
					openId = openidStr
				}

				appId = conf.AppID
			}
		}
	}

	UserMapper := s.UserMapper

	// 使用UnionId作为跨应用统一标识, 如果是wx-OpenId登录，使用OpenId + AppId
	auth := &db.Auth{
		Type:  req.AuthType,
		Value: unionId,
	}
	if req.AuthType == consts.AuthTypeWechatOpenId {
		auth.AppId = appId
		auth.Value = openId
	}
	user, err := UserMapper.FindOneByAuth(ctx, auth)
	switch err {
	case nil: //登录流程
		openAuth := &db.Auth{
			Type:  consts.AuthTypeWechatOpenId,
			Value: openId,
			AppId: appId,
		}
		_, ok := lo.Find(user.Auth, func(item *db.Auth) bool {
			return *item == *openAuth
		})
		if !ok {
			user.Auth = append(user.Auth, openAuth)
			err := UserMapper.Update(ctx, user)
			if err != nil {
				return "", "", "", "", err
			}
		}
		return user.ID.Hex(), unionId, openId, appId, nil
	case consts.ErrNotFound: //注册流程
		auths := []*db.Auth{{
			Type:  consts.AuthTypeWechatOpenId, // OpenId认证
			Value: openId,
			AppId: appId,
		}}
		if unionId != "" {
			auths = append(auths, &db.Auth{
				Type:  consts.AuthTypeWechatUnionId, // UnionId认证
				Value: unionId,
			})
		}
		user = &db.User{Auth: auths}
		err = UserMapper.Insert(ctx, user)
		if err != nil {
			return "", "", "", "", err
		}
		return user.ID.Hex(), unionId, openId, appId, nil
	default:
		return "", "", "", "", err
	}
}

func (s *AuthenticationService) BindWechatOpenId(ctx context.Context, req *sts.AddAuthReq, user *db.User) (*string, error) {
	openId := req.GetVerifyCode()
	appId := req.GetAuthId()
	auth := &db.Auth{
		Type:  consts.AuthTypeWechatOpenId,
		Value: openId,
		AppId: appId,
	}
	_, find := lo.Find(user.Auth, func(item *db.Auth) bool {
		return *item == *auth
	})
	if find {
		return &openId, nil
	}

	_, find = lo.Find(user.Auth, func(item *db.Auth) bool {
		if item.AppId == appId {
			item.Value = openId
			return true
		}
		return false
	})
	if !find {
		user.Auth = append(user.Auth, auth)
	}
	err := s.UserMapper.Update(ctx, user)
	if err != nil {
		return nil, err
	}
	return &openId, nil
}

func (s *AuthenticationService) SetPassword(ctx context.Context, req *sts.SetPasswordReq) (*sts.SetPasswordResp, error) {
	user, err := s.UserMapper.FindOne(ctx, req.UserId)
	if err != nil {
		return nil, err
	}
	hashPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	user.Password = string(hashPassword)
	err = s.UserMapper.Update(ctx, user)
	if err != nil {
		return nil, err
	}
	return &sts.SetPasswordResp{}, nil
}

func (s *AuthenticationService) SendVerifyCode(ctx context.Context, req *sts.SendVerifyCodeReq) (*sts.SendVerifyCodeResp, error) {
	var verifyCode string
	switch req.AuthType {
	case consts.AuthTypeEmail:
		c := s.Config.SMTP
		code, err := rand.Int(rand.Reader, big.NewInt(900000))
		code = code.Add(code, big.NewInt(100000))
		if err != nil {
			return nil, err
		}
		err = sendEmail(c, req.AuthId, code.String())
		if err != nil {
			logx.Error("发送邮件失败")
			return nil, err
		}
		verifyCode = code.String()
	case consts.AuthTypePhone:
		c := s.Config.SMS
		code, err := rand.Int(rand.Reader, big.NewInt(900000))
		code = code.Add(code, big.NewInt(100000))
		if err != nil {
			return nil, err
		}
		phones := make([]string, 0)
		phones = append(phones, req.AuthId)
		err = callSMS(c, phones, code.String())
		if err != nil {
			return nil, err
		}
		verifyCode = code.String()

	default:
		return nil, errors.New("not implement")
	}
	err := s.Redis.SetexCtx(ctx, consts.VerifyCodeKeyPrefix+req.AuthId, verifyCode, 300)
	if err != nil {
		return nil, err
	}
	logx.Infof("向%v:%v 发送验证码: %v", req.AuthType, req.AuthId, verifyCode)
	return &sts.SendVerifyCodeResp{}, nil
}

func callSMS(sms *config.SMSConfig, phones []string, code string) error {
	// 实例化一个认证对象，入参需要传入腾讯云账户 SecretId 和 SecretKey，此处还需注意密钥对的保密
	// 密钥可前往官网控制台 https://console.cloud.tencent.com/cam/capi 进行获取
	credential := common.NewCredential(sms.SecretId, sms.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = sms.Host
	cpf.HttpProfile.ReqMethod = "POST"
	client := common.NewCommonClient(credential, sms.Region, cpf)

	request := tchttp.NewCommonRequest("sms", sms.Version, sms.Action)
	params := make(map[string]interface{})
	params["PhoneNumberSet"] = phones
	params["SmsSdkAppId"] = sms.SmsSdkAppId
	params["TemplateId"] = sms.TemplateId
	params["SignName"] = sms.SignName
	// 模板参数
	codes := make([]string, 0)
	codes = append(codes, code)
	codes = append(codes, "5")
	params["TemplateParamSet"] = codes

	err := request.SetActionParameters(params)
	if err != nil {
		return err
	}

	response := tchttp.NewCommonResponse()
	err = client.Send(request, response)
	if err != nil {
		fmt.Println("fail to invoke api:", err.Error())
		return err
	}

	fmt.Println(string(response.GetBody()))
	return nil
}

func sendEmail(config *struct {
	Username string
	Password string
	Host     string
	Port     int
}, to string, code string) error {
	tlsConfig := &tls.Config{
		ServerName: config.Host,
	}

	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:465", config.Host), tlsConfig)
	if err != nil {
		return fmt.Errorf("SSL连接失败: %v", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return fmt.Errorf("创建SMTP客户端失败: %v", err)
	}
	defer client.Quit()

	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %v", err)
	}

	if err = client.Mail(config.Username); err != nil {
		return fmt.Errorf("设置发送方失败: %v", err)
	}

	if err = client.Rcpt(to); err != nil {
		return fmt.Errorf("设置接收方失败: %v", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("获取邮件写入器失败: %v", err)
	}
	defer writer.Close()

	message := fmt.Sprintf(
		"From: =?UTF-8?B?%s?= <%s>\r\n"+
			"To: %s\r\n"+
			"Subject: =?UTF-8?B?%s?=\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: 8bit\r\n"+
			"\r\n"+
			"您正在进行账号注册，本次注册验证码为：%s\r\n"+
			"验证码5分钟内有效，请勿透露给其他人。\r\n"+
			"\r\n"+
			"此邮件由零界学习平台自动发送，请勿回复。\r\n",
		base64.StdEncoding.EncodeToString([]byte("零界学习")),
		config.Username,
		to,
		base64.StdEncoding.EncodeToString([]byte("【零界学习】验证码")),
		code,
	)

	_, err = writer.Write([]byte(message))
	return err
}
