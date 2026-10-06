package attach

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Attachment 附件元数据；文件本体存 GridFS（attachments.files/chunks），
// 与旧版 attachments 集合（本地磁盘时代）无关。
type Attachment struct {
	ID         bson.ObjectID `bson:"_id,omitempty"`
	TenantID   bson.ObjectID `bson:"tenant_id"`
	OwnerType  string        `bson:"owner_type"` // 如 patient / appointment
	OwnerID    bson.ObjectID `bson:"owner_id"`
	Filename   string        `bson:"filename"`
	Size       int64         `bson:"size"`
	Mime       string        `bson:"mime"`
	UploadedBy string        `bson:"uploaded_by"`
	CreatedAt  time.Time     `bson:"created_at"`
}

// SafeFilename 下载头安全的文件名：取基名，剥引号/反斜杠/控制字符（防 Content-Disposition 断头），
// 中文保留（下载时另拼 filename*=UTF-8）。历史脏数据在下载处同样过一遍。
func SafeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." {
		return "image"
	}
	return name
}

// fileMeta 存在 GridFS 文件文档 metadata 里的业务字段。
type fileMeta struct {
	TenantID   bson.ObjectID `bson:"tenant_id"`
	OwnerType  string        `bson:"owner_type"`
	OwnerID    bson.ObjectID `bson:"owner_id"`
	Mime       string        `bson:"mime"`
	UploadedBy string        `bson:"uploaded_by"`
}

// Service 通用图片上传：只收图片（按内容嗅探，不信任客户端 header），
// 限大小，文件本体进 MongoDB GridFS，随库备份/迁移。
type Service struct {
	bucket    *mongo.GridFSBucket
	maxBytes  int64
	maxImageM int
}

func New(db *mongo.Database, maxImageMB int) *Service {
	if maxImageMB <= 0 {
		maxImageMB = 10
	}
	return &Service{
		bucket:    db.GridFSBucket(options.GridFSBucket().SetName("attachments")),
		maxBytes:  int64(maxImageMB) << 20,
		maxImageM: maxImageMB,
	}
}

// Save 存一张图片，返回元数据。非图片或超限直接拒绝。
func (s *Service) Save(ctx context.Context, tenantID bson.ObjectID, ownerType string, ownerID bson.ObjectID,
	filename string, mime string, r io.Reader, by string) (*Attachment, error) {

	data, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("图片超过%dMB", s.maxImageM)
	}
	if len(data) == 0 {
		return nil, errors.New("空文件")
	}
	sniffed := http.DetectContentType(data)
	if !strings.HasPrefix(sniffed, "image/") {
		return nil, errors.New("只允许上传图片")
	}
	name := filepath.Base(filename)
	if name == "" || name == "." {
		name = "image"
	}
	// 文件名消毒：下载头 filename="..." 里的引号/控制字符可断头注入，
	// 入库前剥掉；展示与下载共用消毒后的名字
	name = SafeFilename(name)
	id, err := s.bucket.UploadFromStream(ctx, name, bytes.NewReader(data),
		options.GridFSUpload().SetMetadata(fileMeta{
			TenantID: tenantID, OwnerType: ownerType, OwnerID: ownerID,
			Mime: sniffed, UploadedBy: by,
		}))
	if err != nil {
		return nil, err
	}
	return &Attachment{
		ID: id, TenantID: tenantID, OwnerType: ownerType, OwnerID: ownerID,
		Filename: name, Size: int64(len(data)), Mime: sniffed,
		UploadedBy: by, CreatedAt: time.Now(),
	}, nil
}

// fileDoc GridFS 文件文档投影（只取列表/下载需要的字段）。
type fileDoc struct {
	ID         bson.ObjectID `bson:"_id"`
	Filename   string        `bson:"filename"`
	Length     int64         `bson:"length"`
	UploadDate time.Time     `bson:"uploadDate"`
	Metadata   fileMeta      `bson:"metadata"`
}

func (d fileDoc) attach() *Attachment {
	return &Attachment{
		ID: d.ID, TenantID: d.Metadata.TenantID, OwnerType: d.Metadata.OwnerType,
		OwnerID: d.Metadata.OwnerID, Filename: d.Filename, Size: d.Length,
		Mime: d.Metadata.Mime, UploadedBy: d.Metadata.UploadedBy, CreatedAt: d.UploadDate,
	}
}

func ownerFilter(tenantID bson.ObjectID, ownerType string, ownerID bson.ObjectID) bson.M {
	return bson.M{
		"metadata.tenant_id":  tenantID,
		"metadata.owner_type": ownerType,
		"metadata.owner_id":   ownerID,
	}
}

// ListByOwner 某归属的图片列表（最新在前）。
func (s *Service) ListByOwner(ctx context.Context, tenantID bson.ObjectID, ownerType string, ownerID bson.ObjectID) ([]Attachment, error) {
	cur, err := s.bucket.Find(ctx, ownerFilter(tenantID, ownerType, ownerID),
		options.GridFSFind().SetSort(bson.D{{Key: "uploadDate", Value: -1}}))
	if err != nil {
		return nil, err
	}
	var docs []fileDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(docs))
	for _, d := range docs {
		out = append(out, *d.attach())
	}
	return out, nil
}

// Open 校验租户后打开下载流（调用方负责 Close）。
func (s *Service) Open(ctx context.Context, tenantID, id bson.ObjectID) (*Attachment, io.ReadCloser, error) {
	var d fileDoc
	err := s.bucket.GetFilesCollection().FindOne(ctx, bson.M{
		"_id":                id,
		"metadata.tenant_id":  tenantID,
	}).Decode(&d)
	if err != nil {
		return nil, nil, err
	}
	stream, err := s.bucket.OpenDownloadStream(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return d.attach(), stream, nil
}

// Delete 删除单张图片（先校验租户归属）。
func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	var d fileDoc
	err := s.bucket.GetFilesCollection().FindOne(ctx, bson.M{
		"_id":               id,
		"metadata.tenant_id": tenantID,
	}).Decode(&d)
	if err != nil {
		return err
	}
	return s.bucket.Delete(ctx, id)
}

// PurgeTenant 删除租户全部图片，删门诊时调用。
func (s *Service) PurgeTenant(ctx context.Context, tenantID bson.ObjectID) error {
	cur, err := s.bucket.Find(ctx, bson.M{"metadata.tenant_id": tenantID})
	if err != nil {
		return err
	}
	var docs []fileDoc
	if err := cur.All(ctx, &docs); err != nil {
		return err
	}
	for _, d := range docs {
		if err := s.bucket.Delete(ctx, d.ID); err != nil {
			return err
		}
	}
	return nil
}
