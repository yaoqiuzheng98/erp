// Package industry 国民经济行业分类（GB/T 4754-2017）：四级行业目录。
// 数据文件 data/industries.json（解析自官方 docx）随仓库分发，
// 集合为空时种子进 MongoDB industries 集合，运行期全量内存缓存。
package industry

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Node 行业节点；Level 1=门类 2=大类 3=中类 4=小类；Path 含自身的祖先码链。
type Node struct {
	Code   string   `bson:"code" json:"code"`
	Name   string   `bson:"name" json:"name"`
	Level  int      `bson:"level" json:"level"`
	Parent string   `bson:"parent" json:"parent"`
	Path   []string `bson:"path" json:"path"` // 如 ["F","52","521","5213"]
	Desc   string   `bson:"desc,omitempty" json:"desc,omitempty"`
	Seq    int      `bson:"seq" json:"seq"`
}

type Service struct {
	col   *mongo.Collection
	mu    sync.RWMutex
	cache map[string]Node // code → node；静态数据，种子后不再变化
	order []Node          // 国标顺序
}

func NewService(db *mongo.Database) *Service {
	return &Service{col: db.Collection("industries")}
}

// EnsureSeed 集合为空时从 JSON 文件导入国标数据（启动时调用一次）。
// 集合已有数据时不读文件——正常启动完全不需要数据文件。
func (s *Service) EnsureSeed(ctx context.Context, path string) error {
	n, err := s.col.EstimatedDocumentCount(ctx)
	if err != nil || n > 0 {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var nodes []Node
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return err
	}
	docs := make([]any, len(nodes))
	for i, nd := range nodes {
		docs[i] = nd
	}
	if _, err := s.col.InsertMany(ctx, docs); err != nil {
		return err
	}
	_, err = s.col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "code", Value: 1}},
	})
	return err
}

// load 全量加载进缓存（幂等）。
func (s *Service) load(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil {
		return
	}
	s.cache = map[string]Node{}
	cur, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}))
	if err != nil {
		return
	}
	var nodes []Node
	if err := cur.All(ctx, &nodes); err != nil {
		return
	}
	for _, nd := range nodes {
		s.cache[nd.Code] = nd
		s.order = append(s.order, nd)
	}
}

func (s *Service) ensure(ctx context.Context) {
	s.mu.RLock()
	ok := s.cache != nil
	s.mu.RUnlock()
	if !ok {
		s.load(ctx)
	}
}

// Get 按码取节点；未知码返回 false。
func (s *Service) Get(ctx context.Context, code string) (Node, bool) {
	s.ensure(ctx)
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.cache[code]
	return n, ok
}

// All 按国标顺序返回全部节点（约 2000 条）。
func (s *Service) All(ctx context.Context) []Node {
	s.ensure(ctx)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Node{}, s.order...)
}

// Name 返回码对应名称；空码 = 通用，未知码原样返回。
func (s *Service) Name(ctx context.Context, code string) string {
	if code == "" {
		return "通用"
	}
	if n, ok := s.Get(ctx, code); ok {
		return n.Name
	}
	return code
}

// PathName 返回祖先名链（如 "批发和零售业 > 零售业 > 综合零售 > 便利店零售"）。
func (s *Service) PathName(ctx context.Context, code string) string {
	if code == "" {
		return "通用"
	}
	n, ok := s.Get(ctx, code)
	if !ok {
		return code
	}
	names := make([]string, len(n.Path))
	for i, c := range n.Path {
		names[i] = s.Name(ctx, c)
	}
	return strings.Join(names, " > ")
}

// Exists 校验码是否存在（含空码 = 通用）。
func (s *Service) Exists(ctx context.Context, code string) bool {
	if code == "" {
		return true
	}
	_, ok := s.Get(ctx, code)
	return ok
}
