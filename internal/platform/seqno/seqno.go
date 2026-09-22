package seqno

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Generator 单据编号器：按 租户+规则+年月 分段原子发号，格式 RULE-202609-0001。
type Generator struct {
	col *mongo.Collection
}

func New(db *mongo.Database) *Generator {
	return &Generator{col: db.Collection("sequences")}
}

func (g *Generator) Next(ctx context.Context, tenantID bson.ObjectID, rule string) (string, error) {
	ym := time.Now().Format("200601")
	key := fmt.Sprintf("%s:%s:%s", tenantID.Hex(), rule, ym)
	var doc struct {
		Value int64 `bson:"value"`
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	err := g.col.FindOneAndUpdate(ctx,
		bson.M{"_id": key},
		bson.M{"$inc": bson.M{"value": 1}},
		opts,
	).Decode(&doc)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%04d", rule, ym, doc.Value), nil
}
