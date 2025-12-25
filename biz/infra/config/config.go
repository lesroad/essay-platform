package config

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// //go:embed config.local.yaml
var embeddedConfig []byte

type CosConfig struct {
	AppId      string
	BucketName string
	Region     string
	SecretId   string
	SecretKey  string
}

func (c *CosConfig) CosHost() string {
	return fmt.Sprintf("https://%s.cos.%s.myqcloud.com", c.BucketName, c.Region)
}

func (c *CosConfig) CIHost() string {
	return fmt.Sprintf("https://%s.ci.%s.myqcloud.com", c.BucketName, c.Region)
}

type Config struct {
	service.ServiceConf
	ListenOn                 string
	CosConfig                *CosConfig
	CacheConf                cache.CacheConf
	WechatApplicationConfigs []*WechatApplicationConfig
	WechatPayConfig          *WechatPayConfig
	Redis                    *redis.RedisConf
	WeChatRedis              *redis.RedisConf
	SMTP                     *struct {
		Username string
		Password string
		Host     string
		Port     int
	}
	Mongo *struct {
		DB  string
		URL string
	}
	SMS *SMSConfig
}

type SMSConfig struct {
	SecretId    string
	SecretKey   string
	Host        string
	Action      string
	Version     string
	Region      string
	SmsSdkAppId string
	TemplateId  string
	SignName    string // 短信签名内容，使用 UTF-8 编码，必须填写已审核通过的签名，例如：腾讯云，签名信息可前往 国内短信 或 国际/港澳台短信 的签名管理查看。 注意 发送国内短信该参数必填，且需填写签名内容而非签名ID。
}

type DefaultWechatUser struct {
	AppId  string
	OpenId string
}

type WechatApplicationConfig struct {
	AppID     string
	AppSecret string
	Type      string
}

type WechatPayConfig struct {
	MchId             string
	NotifyURL         string
	PayURL            string
	ApiclientKey      string
	ApiclientSerialNo string
}

func NewConfig() (*Config, error) {
	c := new(Config)

	// 优先使用环境变量指定的配置文件
	path := os.Getenv("CONFIG_PATH")
	if path != "" {
		err := conf.Load(path, c)
		if err != nil {
			return nil, err
		}
	} else {
		// 使用嵌入的配置文件
		err := conf.LoadFromYamlBytes(embeddedConfig, c)
		if err != nil {
			return nil, err
		}
	}

	err := c.SetUp()
	if err != nil {
		return nil, err
	}
	return c, nil
}
