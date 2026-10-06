package repo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var errInvalidID = errors.New("写入失败: 非法 ID")

// TenantRepo 模板方法基类：写路径注入 tenant_id，读路径强制过滤。
type TenantRepo[T any] struct {
	Col *mongo.Collection
}

func NewTenantRepo[T any](db *mongo.Database, name string) *TenantRepo[T] {
	return &TenantRepo[T]{Col: db.Collection(name)}
}

func scoped(filter bson.M, tenantID bson.ObjectID) bson.M {
	if filter == nil {
		filter = bson.M{}
	}
	filter["tenant_id"] = tenantID
	return filter
}

func (r *TenantRepo[T]) FindMany(ctx context.Context, tenantID bson.ObjectID, filter bson.M, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	cur, err := r.Col.Find(ctx, scoped(filter, tenantID), opts...)
	if err != nil {
		return nil, err
	}
	var out []T
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *TenantRepo[T]) FindOne(ctx context.Context, tenantID bson.ObjectID, filter bson.M) (*T, error) {
	var out T
	err := r.Col.FindOne(ctx, scoped(filter, tenantID)).Decode(&out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo[T]) FindByID(ctx context.Context, tenantID, id bson.ObjectID) (*T, error) {
	return r.FindOne(ctx, tenantID, bson.M{"_id": id})
}

// Insert 把文档写为 bson.M 以便强制注入租户字段与时间戳。
func (r *TenantRepo[T]) Insert(ctx context.Context, tenantID bson.ObjectID, doc *T) (bson.ObjectID, error) {
	raw, err := bson.Marshal(doc)
	if err != nil {
		return bson.NilObjectID, err
	}
	var m bson.M
	if err := bson.Unmarshal(raw, &m); err != nil {
		return bson.NilObjectID, err
	}
	m["tenant_id"] = tenantID
	m["created_at"] = time.Now()
	res, err := r.Col.InsertOne(ctx, m)
	if err != nil {
		return bson.NilObjectID, err
	}
	id, ok := res.InsertedID.(bson.ObjectID)
	if !ok {
		return bson.NilObjectID, errInvalidID
	}
	return id, nil
}

func (r *TenantRepo[T]) Update(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	set["updated_at"] = time.Now()
	_, err := r.Col.UpdateOne(ctx, scoped(bson.M{"_id": id}, tenantID), bson.M{"$set": set})
	return err
}

func (r *TenantRepo[T]) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := r.Col.DeleteOne(ctx, scoped(bson.M{"_id": id}, tenantID))
	return err
}

// UpdateWhere 条件更新（状态机流转/乐观并发防竞态），返回命中数。
// 命中 0 表示条件已不成立（被别人先处理了），调用方应报错而非继续。
func (r *TenantRepo[T]) UpdateWhere(ctx context.Context, tenantID bson.ObjectID, filter bson.M, set bson.M) (int64, error) {
	set["updated_at"] = time.Now()
	res, err := r.Col.UpdateOne(ctx, scoped(filter, tenantID), bson.M{"$set": set})
	if err != nil {
		return 0, err
	}
	return res.MatchedCount, nil
}

func (r *TenantRepo[T]) Count(ctx context.Context, tenantID bson.ObjectID, filter bson.M) (int64, error) {
	return r.Col.CountDocuments(ctx, scoped(filter, tenantID))
}

// SysRepo 不做租户隔离，仅用于系统级数据（sys_admins / tenants 等）。
type SysRepo[T any] struct {
	Col *mongo.Collection
}

func NewSysRepo[T any](db *mongo.Database, name string) *SysRepo[T] {
	return &SysRepo[T]{Col: db.Collection(name)}
}

func (r *SysRepo[T]) FindMany(ctx context.Context, filter bson.M, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	cur, err := r.Col.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	var out []T
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *SysRepo[T]) FindOne(ctx context.Context, filter bson.M) (*T, error) {
	var out T
	err := r.Col.FindOne(ctx, filter).Decode(&out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
