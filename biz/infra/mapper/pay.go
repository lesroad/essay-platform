package mapper

import (
	"context"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"math/rand"
	"time"

	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	PayCollectionName = "sts_pay"
	letters           = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits            = "0123456789"
)

type PayMapper struct {
	conn *monc.Model
}

type Pay struct {
	ID      primitive.ObjectID `bson:"_id"`
	OrderId string             `bson:"order_id"`
	AppId   string             `bson:"app_id"`
	UserId  string             `bson:"user_id"`
	Order   *Order             `bson:"order"`
	Payment *Payment           `bson:"payment,omitempty"`
}

type Order struct {
	Amount        int64     `bson:"amount"`
	StatusMessage string    `bson:"status_message"`
	CreateTime    time.Time `bson:"create_time"`
}

type Payment struct {
	CreateTime time.Time `bson:"create_time"`
}

func NewPayMapper(config *config.Config) *PayMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, PayCollectionName, config.CacheConf)
	return &PayMapper{
		conn: conn,
	}
}

func (m *PayMapper) Insert(ctx context.Context, appId string, userId string, amount int64) (*Pay, error) {
	var orderId string
	for i := 0; i < 100; i++ {
		orderId = genOrderId()
		pay, err := m.FindOne(ctx, orderId)
		if err == nil && pay != nil {
			continue
		}
		if err == consts.ErrNotFound {
			break
		}
		return nil, err
	}
	pay := &Pay{
		ID:      primitive.NewObjectID(),
		OrderId: orderId,
		AppId:   appId,
		UserId:  userId,
		Order: &Order{
			Amount:     amount,
			CreateTime: time.Now(),
		},
	}
	_, err := m.conn.InsertOneNoCache(ctx, pay)
	return pay, err
}

func (m *PayMapper) Update(ctx context.Context, pay *Pay) error {
	_, err := m.conn.UpdateOneNoCache(ctx, bson.M{"_id": pay.ID}, bson.M{
		"$set": pay,
	})
	return err
}

func (m *PayMapper) FindOne(ctx context.Context, orderId string) (*Pay, error) {
	pay := &Pay{}
	err := m.conn.FindOneNoCache(ctx, pay, bson.M{"order_id": orderId})
	switch err {
	case nil:
		return pay, nil
	case monc.ErrNotFound:
		return nil, consts.ErrNotFound
	default:
		return nil, err
	}
}

func genOrderId() string {
	letterPart := make([]byte, 8)
	for i := range letterPart {
		letterPart[i] = letters[rand.Intn(len(letters))]
	}
	digitPart := make([]byte, 8)
	for i := range digitPart {
		digitPart[i] = digits[rand.Intn(len(digits))]
	}
	code := append(letterPart, digitPart...)
	rand.Shuffle(len(code), func(i, j int) {
		code[i], code[j] = code[j], code[i]
	})
	return string(code)
}
