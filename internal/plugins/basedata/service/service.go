package service

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/contract"
	prepo "erp/internal/platform/repo"
	"erp/internal/plugins/basedata/model"
	"erp/internal/plugins/basedata/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var ErrDupName = errors.New("同名已存在")

// Service 实现 contract.MasterDataAPI，同时承担插件内部 CRUD。
type Service struct {
	products   *repo.ProductRepo
	warehouses *prepo.TenantRepo[model.Warehouse]
	customers  *prepo.TenantRepo[model.Customer]
	suppliers  *prepo.TenantRepo[model.Supplier]
}

func New(db *mongo.Database) *Service {
	return &Service{
		products:   repo.NewProductRepo(db),
		warehouses: repo.NewWarehouseRepo(db),
		customers:  repo.NewCustomerRepo(db),
		suppliers:  repo.NewSupplierRepo(db),
	}
}

// ---------- 商品 ----------

func (s *Service) ListProducts(ctx context.Context, tenantID bson.ObjectID, kw string, skip, limit int64) ([]model.Product, int64, error) {
	f := bson.M{}
	if kw != "" {
		f["name"] = bson.M{"$regex": kw}
	}
	total, err := s.products.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.products.Search(ctx, tenantID, kw, skip, limit)
	return list, total, err
}

func (s *Service) CreateProduct(ctx context.Context, tenantID bson.ObjectID, p *model.Product, by string) error {
	if p.Name == "" {
		return errors.New("名称必填")
	}
	if dup, _ := s.products.FindOne(ctx, tenantID, bson.M{"name": p.Name}); dup != nil {
		return ErrDupName
	}
	p.TenantID, p.CreatedAt, p.CreatedBy, p.Status = tenantID, time.Now(), by, "active"
	_, err := s.products.Insert(ctx, tenantID, p)
	return err
}

func (s *Service) UpdateProduct(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	return s.products.Update(ctx, tenantID, id, set)
}

func (s *Service) ProductByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.Product, error) {
	return s.products.FindByID(ctx, tenantID, id)
}

// ---------- 仓库/客户/供应商 ----------

func (s *Service) ListWarehouses(ctx context.Context, tenantID bson.ObjectID) ([]model.Warehouse, error) {
	return s.warehouses.FindMany(ctx, tenantID, bson.M{})
}

func (s *Service) CreateWarehouse(ctx context.Context, tenantID bson.ObjectID, w *model.Warehouse) error {
	w.TenantID, w.CreatedAt = tenantID, time.Now()
	_, err := s.warehouses.Insert(ctx, tenantID, w)
	return err
}

func (s *Service) ListCustomers(ctx context.Context, tenantID bson.ObjectID) ([]model.Customer, error) {
	return s.customers.FindMany(ctx, tenantID, bson.M{})
}

// CreateCustomer 建客户（contract.MasterDataAPI；handler 与跨插件调用共用）。
func (s *Service) CreateCustomer(ctx context.Context, tenantID bson.ObjectID, in contract.CustomerUpsert) (*contract.PartnerRef, error) {
	if in.Name == "" {
		return nil, errors.New("名称必填")
	}
	cu := &model.Customer{Name: in.Name, Contact: in.Contact, Phone: in.Phone}
	cu.TenantID, cu.CreatedAt = tenantID, time.Now()
	id, err := s.customers.Insert(ctx, tenantID, cu)
	if err != nil {
		return nil, err
	}
	return &contract.PartnerRef{ID: id, Name: cu.Name, Phone: cu.Phone}, nil
}

func (s *Service) ListSuppliers(ctx context.Context, tenantID bson.ObjectID) ([]model.Supplier, error) {
	return s.suppliers.FindMany(ctx, tenantID, bson.M{})
}

func (s *Service) CreateSupplier(ctx context.Context, tenantID bson.ObjectID, su *model.Supplier) error {
	su.TenantID, su.CreatedAt = tenantID, time.Now()
	_, err := s.suppliers.Insert(ctx, tenantID, su)
	return err
}

// ---------- contract.MasterDataAPI 实现 ----------

func (s *Service) Product(ctx context.Context, tenantID, id bson.ObjectID) (*contract.ProductRef, error) {
	p, err := s.products.FindByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return &contract.ProductRef{
		ID: p.ID, Name: p.Name, Unit: p.Unit,
		Price: p.Price, MinStock: p.MinStock,
	}, nil
}

func (s *Service) Products(ctx context.Context, tenantID bson.ObjectID) ([]contract.ProductRef, error) {
	list, err := s.products.FindMany(ctx, tenantID, bson.M{"status": "active"})
	if err != nil {
		return nil, err
	}
	out := make([]contract.ProductRef, 0, len(list))
	for _, p := range list {
		out = append(out, contract.ProductRef{
			ID: p.ID, Name: p.Name, Unit: p.Unit,
			Price: p.Price, MinStock: p.MinStock,
		})
	}
	return out, nil
}

func (s *Service) Warehouses(ctx context.Context, tenantID bson.ObjectID) ([]contract.WarehouseRef, error) {
	list, err := s.ListWarehouses(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]contract.WarehouseRef, 0, len(list))
	for _, w := range list {
		out = append(out, contract.WarehouseRef{ID: w.ID, Name: w.Name})
	}
	return out, nil
}

func (s *Service) Customers(ctx context.Context, tenantID bson.ObjectID) ([]contract.PartnerRef, error) {
	list, err := s.ListCustomers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]contract.PartnerRef, 0, len(list))
	for _, c := range list {
		out = append(out, contract.PartnerRef{ID: c.ID, Name: c.Name, Phone: c.Phone})
	}
	return out, nil
}

// Customer 单查（contract.MasterDataAPI）。
func (s *Service) Customer(ctx context.Context, tenantID, id bson.ObjectID) (*contract.PartnerRef, error) {
	c, err := s.customers.FindByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return &contract.PartnerRef{ID: c.ID, Name: c.Name, Phone: c.Phone}, nil
}

func (s *Service) Suppliers(ctx context.Context, tenantID bson.ObjectID) ([]contract.PartnerRef, error) {
	list, err := s.ListSuppliers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]contract.PartnerRef, 0, len(list))
	for _, su := range list {
		out = append(out, contract.PartnerRef{ID: su.ID, Name: su.Name})
	}
	return out, nil
}

// EnsureIndexes 插件安装钩子调用：去编码迁移——删掉历史 {tenant_id, code}
// 唯一索引（无编码后多文档缺字段会在唯一索引下冲突），并清理存量 code 字段。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_ = s.products.Col.Indexes().DropOne(ctx, "tenant_id_1_code_1")
	for _, col := range []*mongo.Collection{
		s.products.Col, s.warehouses.Col, s.customers.Col, s.suppliers.Col,
	} {
		_, _ = col.UpdateMany(ctx, bson.M{"code": bson.M{"$exists": true}},
			bson.M{"$unset": bson.M{"code": ""}})
	}
	return nil
}
