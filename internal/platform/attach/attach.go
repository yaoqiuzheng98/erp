package attach

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Attachment 附件元数据；文件本体落本地磁盘。
type Attachment struct {
	ID         bson.ObjectID `bson:"_id,omitempty"`
	TenantID   bson.ObjectID `bson:"tenant_id"`
	OwnerType  string        `bson:"owner_type"` // 如 product / order
	OwnerID    bson.ObjectID `bson:"owner_id"`
	Filename   string        `bson:"filename"`
	Path       string        `bson:"path"`
	Size       int64         `bson:"size"`
	Mime       string        `bson:"mime"`
	UploadedBy string        `bson:"uploaded_by"`
	CreatedAt  time.Time     `bson:"created_at"`
}

// Service 策略模式：当前实现为本地磁盘，可替换 GridFS 实现。
type Service struct {
	col *mongo.Collection
	dir string
}

func New(db *mongo.Database, uploadDir string) *Service {
	return &Service{col: db.Collection("attachments"), dir: uploadDir}
}

func (s *Service) Save(ctx context.Context, tenantID bson.ObjectID, ownerType string, ownerID bson.ObjectID,
	filename string, mime string, r io.Reader, by string) (*Attachment, error) {

	sub := filepath.Join(tenantID.Hex(), ownerType, ownerID.Hex())
	dir := filepath.Join(s.dir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stored := fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(filename))
	full := filepath.Join(dir, stored)
	f, err := os.Create(full)
	if err != nil {
		return nil, err
	}
	size, err := io.Copy(f, r)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	a := &Attachment{
		TenantID: tenantID, OwnerType: ownerType, OwnerID: ownerID,
		Filename: filename, Path: filepath.Join(sub, stored),
		Size: size, Mime: mime, UploadedBy: by, CreatedAt: time.Now(),
	}
	res, err := s.col.InsertOne(ctx, a)
	if err != nil {
		return nil, err
	}
	a.ID = res.InsertedID.(bson.ObjectID)
	return a, nil
}

func (s *Service) ListByOwner(ctx context.Context, tenantID bson.ObjectID, ownerType string, ownerID bson.ObjectID) ([]Attachment, error) {
	cur, err := s.col.Find(ctx, bson.M{
		"tenant_id": tenantID, "owner_type": ownerType, "owner_id": ownerID,
	})
	if err != nil {
		return nil, err
	}
	var out []Attachment
	return out, cur.All(ctx, &out)
}

// Get 校验租户后返回磁盘文件完整路径。
func (s *Service) Get(ctx context.Context, tenantID, id bson.ObjectID) (*Attachment, string, error) {
	var a Attachment
	err := s.col.FindOne(ctx, bson.M{"_id": id, "tenant_id": tenantID}).Decode(&a)
	if err != nil {
		return nil, "", err
	}
	return &a, filepath.Join(s.dir, a.Path), nil
}
