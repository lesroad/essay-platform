package provider

import (
	"essay-platform/biz/application/service"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/mapper"
	"essay-platform/biz/infra/sdk/cos"
	"essay-platform/biz/infra/sdk/wechat"
	"essay-platform/biz/infra/stores/redis"

	"github.com/google/wire"
)

type Provider struct {
	Config         *config.Config
	CosService     service.CosService
	AuthService    service.AuthenticationService
	MessageService service.MessageService
	PayService     service.PayService
}

var provider *Provider

func Init() {
	var err error
	provider, err = NewProvider()
	if err != nil {
		panic(err)
	}
}

func Get() *Provider {
	return provider
}

var AllProvider = wire.NewSet(
	ApplicationSet,
	InfrastructureSet,
)

var ApplicationSet = wire.NewSet(
	service.CosSet,
	service.AuthenticationSet,
	service.MessageSet,
	service.PaySet,
)

var InfrastructureSet = wire.NewSet(
	config.NewConfig,
	redis.NewRedis,
	mapper.NewUserMapper,
	mapper.NewPayMapper,
	cos.NewCosSDK,
	wechat.NewWechatApplicationMap,
)
