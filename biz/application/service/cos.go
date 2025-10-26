package service

import (
	"context"
	"essay-platform/biz/application/dto/sts"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"essay-platform/biz/infra/mapper"
	"essay-platform/biz/infra/sdk/cos"
	"essay-platform/biz/infra/sdk/wechat"
	"fmt"
	"time"

	"github.com/google/wire"
	cossts "github.com/tencentyun/qcloud-cos-sts-sdk/go"
)

type ICosService interface {
	GenCosSts(ctx context.Context, req *sts.GenCosStsReq) (*sts.GenCosStsResp, error)
	GenSignedUrl(ctx context.Context, req *sts.GenSignedUrlReq) (*sts.GenSignedUrlResp, error)
	DeleteObject(ctx context.Context, req *sts.DeleteObjectReq) (*sts.DeleteObjectResp, error)
}

type CosService struct {
	Config         *config.Config
	CosSDK         *cos.CosSDK
	UrlMapper      mapper.UrlMapper
	UserMapper     *mapper.UserMapper
	MiniProgramMap wechat.MiniProgramMap
}

var CosSet = wire.NewSet(
	wire.Struct(new(CosService), "*"),
	wire.Bind(new(ICosService), new(*CosService)),
)

// GenCosSts 生成COS的临时密钥(TmpSecretId + TmpSecretKey + Token) https://cloud.tencent.com/document/product/436/14048
// 使用临时密钥访问COS: https://cloud.tencent.com/document/product/436/68283
func (s *CosService) GenCosSts(ctx context.Context, req *sts.GenCosStsReq) (*sts.GenCosStsResp, error) {
	cosConfig := s.Config.CosConfig
	stsOption := &cossts.CredentialOptions{
		DurationSeconds: int64(60 * time.Minute.Seconds()),
		Region:          cosConfig.Region,
		Policy: &cossts.CredentialPolicy{
			Statement: []cossts.CredentialPolicyStatement{
				{
					// 密钥的权限列表。简单上传和分片需要以下的权限，其他权限列表请看 https://cloud.tencent.com/document/product/436/31923
					Action: []string{
						// 简单上传
						"name/cos:PostObject",
						"name/cos:PutObject",
						// 分片上传
						"name/cos:InitiateMultipartUpload",
						"name/cos:ListMultipartUploads",
						"name/cos:ListParts",
						"name/cos:UploadPart",
						"name/cos:CompleteMultipartUpload",
					},
					Effect: "allow",
					// 密钥可控制的资源列表。此处开放名字为用户ID的文件夹及其子文件夹
					Resource: []string{
						fmt.Sprintf("qcs::cos:%s:uid/%s:%s/%s",
							cosConfig.Region, cosConfig.AppId, cosConfig.BucketName, req.Path),
					},
				},
				{
					Action: []string{
						//下载操作
						"name/cos:GetObject",
					},
					Effect: "allow",
					Resource: []string{
						fmt.Sprintf("qcs::cos:%s:uid/%s:%s/%s",
							cosConfig.Region, cosConfig.AppId, cosConfig.BucketName, req.Path),
					},
				},
			},
		},
	}

	// 请求临时密钥
	res, err := s.CosSDK.GetCredential(ctx, stsOption)
	if err != nil {
		return nil, err
	}

	return &sts.GenCosStsResp{
		SecretId:     res.Credentials.TmpSecretID,
		SecretKey:    res.Credentials.TmpSecretKey,
		SessionToken: res.Credentials.SessionToken,
		ExpiredTime:  int64(res.ExpiredTime),
		StartTime:    int64(res.StartTime),
	}, nil
}

// GenSignedUrl 生成预签名URL(URL + Token)
// 使用预签名URL访问COS: https://cloud.tencent.com/document/product/436/68284
// 如果是临时密钥生成的预签名，下载/上传时需要携带x-cos-security-token；永久密钥生成的预签名不需要带x-cos-security-token
func (s *CosService) GenSignedUrl(ctx context.Context, req *sts.GenSignedUrlReq) (*sts.GenSignedUrlResp, error) {
	signedUrl, err := s.CosSDK.GetPresignedURL(ctx, req.Method, req.Path, req.SecretId, req.SecretKey, 60*time.Minute, nil)
	if err != nil {
		return nil, err
	}
	//s.SendDelayMessage(s.Config, signedUrl)
	return &sts.GenSignedUrlResp{SignedUrl: signedUrl.String()}, nil
}

func (s *CosService) DeleteObject(ctx context.Context, req *sts.DeleteObjectReq) (*sts.DeleteObjectResp, error) {
	res, err := s.CosSDK.Delete(ctx, req.Path)
	if err != nil || res.StatusCode != 200 {
		return nil, consts.ErrCannotDeleteObject
	}
	return &sts.DeleteObjectResp{}, nil
}
