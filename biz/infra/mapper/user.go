package mapper

import (
	"context"
	"essay-platform/biz/infra/config"
	"essay-platform/biz/infra/consts"
	"essay-platform/biz/infra/data/db"
	"time"

	"github.com/zeromicro/go-zero/core/stores/monc"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	UserCollectionName = "sts_user"
	prefixUserCacheKey = "cache:auth:"
)

type UserMapper struct {
	conn *monc.Model
}

// NewUserMapper returns a mapper for the mongo.
func NewUserMapper(config *config.Config) *UserMapper {
	conn := monc.MustNewModel(config.Mongo.URL, config.Mongo.DB, UserCollectionName, config.CacheConf)
	return &UserMapper{
		conn: conn,
	}
}

func (m *UserMapper) FindOneByAuth(ctx context.Context, auth *db.Auth) (*db.User, error) {
	var data db.User
	err := m.conn.FindOneNoCache(ctx, &data, bson.M{"auth": auth})
	switch err {
	case nil:
		return &data, nil
	case monc.ErrNotFound:
		return nil, consts.ErrNotFound
	default:
		return nil, err
	}
}

func (m *UserMapper) Insert(ctx context.Context, data *db.User) error {
	if data.ID.IsZero() {
		data.ID = primitive.NewObjectID()
		data.CreateAt = time.Now()
		data.UpdateAt = time.Now()
	}

	key := prefixUserCacheKey + data.ID.Hex()
	_, err := m.conn.InsertOne(ctx, key, data)
	return err
}

func (m *UserMapper) FindOne(ctx context.Context, id string) (*db.User, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, consts.ErrInvalidObjectId
	}

	var data db.User
	//key := prefixUserCacheKey + id
	//err = m.conn.FindOne(ctx, key, &data, bson.M{"_id": oid})
	err = m.conn.FindOneNoCache(ctx, &data, bson.M{"_id": oid})
	switch err {
	case nil:
		return &data, nil
	case monc.ErrNotFound:
		return nil, consts.ErrNotFound
	default:
		return nil, err
	}
}

func (m *UserMapper) Update(ctx context.Context, data *db.User) error {
	data.UpdateAt = time.Now()
	key := prefixUserCacheKey + data.ID.Hex()
	_, err := m.conn.UpdateOne(ctx, key, bson.M{"_id": data.ID}, bson.M{"$set": data})
	return err
}

func (m *UserMapper) Delete(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return consts.ErrInvalidObjectId
	}
	key := prefixUserCacheKey + id
	_, err = m.conn.DeleteOne(ctx, key, bson.M{"_id": oid})
	return err
}
