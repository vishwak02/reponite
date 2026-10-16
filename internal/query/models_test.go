package query

import (
	"strings"
	"testing"
)

func modelByName(t *testing.T, ms []DataModel, name string) DataModel {
	t.Helper()
	for _, m := range ms {
		if m.Name == name {
			return m
		}
	}
	var have []string
	for _, m := range ms {
		have = append(have, m.Name+"/"+m.Framework)
	}
	t.Fatalf("no model %q (have %v)", name, have)
	return DataModel{}
}

func fieldByName(t *testing.T, m DataModel, name string) DataField {
	t.Helper()
	for _, f := range m.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("model %s has no field %q (fields %+v)", m.Name, name, m.Fields)
	return DataField{}
}

func relByField(t *testing.T, m DataModel, field string) DataRelation {
	t.Helper()
	for _, r := range m.Relations {
		if r.Field == field {
			return r
		}
	}
	t.Fatalf("model %s has no relation on %q (relations %+v)", m.Name, field, m.Relations)
	return DataRelation{}
}

const djangoModels = `from django.db import models


class TimeStamped(models.Model):
    created = models.DateTimeField(auto_now_add=True)

    class Meta:
        abstract = True


class Customer(TimeStamped):
    email = models.EmailField(unique=True)
    name = models.CharField(
        max_length=80,
        null=True,
        default="anon",
    )


class Order(TimeStamped):
    customer = models.ForeignKey(Customer, on_delete=models.CASCADE, related_name="orders")
    tags = models.ManyToManyField("shop.Tag", blank=True)
    number = models.CharField(max_length=20, db_index=True)
    objects = models.Manager()
    open_orders = OpenManager()

    class Meta:
        db_table = "orders"


class Tag(models.Model):
    label = models.SlugField(primary_key=True)
`

const drfSerializers = `from rest_framework import serializers
from shop.models import Order


class OrderSerializer(serializers.ModelSerializer):
    total = serializers.DecimalField(max_digits=8, decimal_places=2)

    class Meta:
        model = Order
        fields = ["id", "customer", "number"]


class UploadSerializer(serializers.Serializer):
    file = serializers.FileField()
    dry_run = serializers.BooleanField(default=False)
`

func TestDjangoAndDRFModels(t *testing.T) {
	ms, sers := detectModels("shop", []File{{Path: "shop/models.py", Content: djangoModels}, {Path: "shop/serializers.py", Content: drfSerializers}})
	ts := modelByName(t, ms, "TimeStamped")
	if ts.Kind != KindAbstract || ts.Table != "" {
		t.Errorf("abstract base: %+v", ts)
	}
	c := modelByName(t, ms, "Customer")
	if c.Table != "shop_customer" || c.Kind != KindTable {
		t.Errorf("customer table %q kind %q", c.Table, c.Kind)
	}
	if f := fieldByName(t, c, "name"); !f.Null || f.Default != `"anon"` {
		t.Errorf("multi-line field options: %+v", f)
	}
	if f := fieldByName(t, c, "created"); f.Inherited != "TimeStamped" {
		t.Errorf("inherited field: %+v", f)
	}
	if f := fieldByName(t, c, "id"); !f.PK || !f.Implicit {
		t.Errorf("implicit id: %+v", f)
	}
	o := modelByName(t, ms, "Order")
	if o.Table != "orders" {
		t.Errorf("db_table: %q", o.Table)
	}
	if r := relByField(t, o, "customer"); r.Kind != "fk" || r.Target != "Customer" || r.Reverse != "orders" {
		t.Errorf("fk: %+v", r)
	}
	if r := relByField(t, o, "tags"); r.Kind != "m2m" || r.Target != "Tag" {
		t.Errorf("m2m by string ref: %+v", r)
	}
	if f := fieldByName(t, o, "customer"); f.Column != "customer_id" || !f.Index {
		t.Errorf("fk column: %+v", f)
	}
	if f := fieldByName(t, o, "number"); !f.Index {
		t.Errorf("db_index: %+v", f)
	}
	if len(o.managers) != 1 || o.managers[0] != "open_orders" {
		t.Errorf("managers: %v", o.managers)
	}
	if f := fieldByName(t, modelByName(t, ms, "Tag"), "label"); !f.PK {
		t.Errorf("pk: %+v", f)
	}
	if len(sers) != 1 || sers[0].Name != "OrderSerializer" || sers[0].Model != "Order" || !containsStr(sers[0].Fields, "total") {
		t.Errorf("serializers: %+v", sers)
	}
	up := modelByName(t, ms, "UploadSerializer")
	if up.Kind != KindSchema || up.Framework != "drf" || len(up.Fields) != 2 {
		t.Errorf("plain serializer as schema: %+v", up)
	}
}

func TestSQLAlchemyPydanticDataclass(t *testing.T) {
	sa := `from sqlalchemy import Column, ForeignKey, Integer, String
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship

class Base(DeclarativeBase):
    pass

class User(Base):
    __tablename__ = "users"
    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[Optional[str]] = mapped_column(String(50))
    addresses: Mapped[List["Address"]] = relationship(back_populates="user")

class Address(Base):
    __tablename__ = "addresses"
    id = Column(Integer, primary_key=True)
    email = Column(String, nullable=False, unique=True)
    user_id = Column(Integer, ForeignKey("users.id"))
    user = relationship("User", back_populates="addresses")
`
	pyd := `from pydantic import BaseModel, Field
from dataclasses import dataclass

class OrderIn(BaseModel):
    number: str
    note: Optional[str] = None
    lines: List[LineIn] = Field(default_factory=list)

class LineIn(BaseModel):
    sku: str
    qty: int = 1

@dataclass
class Point:
    x: float
    y: float = 0.0
`
	ms, _ := detectModels("app", []File{{Path: "app/db.py", Content: sa}, {Path: "app/schemas.py", Content: pyd}})
	for _, m := range ms {
		if m.Name == "Base" {
			t.Errorf("declarative base listed as a model: %+v", m)
		}
	}
	u := modelByName(t, ms, "User")
	if u.Table != "users" || u.Framework != "sqlalchemy" {
		t.Errorf("user: %+v", u)
	}
	if f := fieldByName(t, u, "id"); !f.PK {
		t.Errorf("mapped pk: %+v", f)
	}
	if f := fieldByName(t, u, "name"); !f.Null {
		t.Errorf("Optional → nullable: %+v", f)
	}
	if r := relByField(t, u, "addresses"); r.Kind != "to_many" || r.Target != "Address" || r.Reverse != "user" {
		t.Errorf("relationship: %+v", r)
	}
	a := modelByName(t, ms, "Address")
	if f := fieldByName(t, a, "email"); f.Null || !f.Unique {
		t.Errorf("column options: %+v", f)
	}
	if r := relByField(t, a, "user_id"); r.Kind != "fk" || r.Target != "User" {
		t.Errorf("ForeignKey(users.id) by table: %+v", r)
	}
	o := modelByName(t, ms, "OrderIn")
	if o.Kind != KindSchema || o.Framework != "pydantic" {
		t.Errorf("pydantic: %+v", o)
	}
	if f := fieldByName(t, o, "note"); !f.Null {
		t.Errorf("optional schema field: %+v", f)
	}
	if r := relByField(t, o, "lines"); r.Kind != "ref_many" || r.Target != "LineIn" {
		t.Errorf("nested schema ref: %+v", r)
	}
	p := modelByName(t, ms, "Point")
	if p.Framework != "dataclass" || fieldByName(t, p, "y").Default != "0.0" {
		t.Errorf("dataclass: %+v", p)
	}
}

func TestProjectBaseModelIsDjangoNotPydantic(t *testing.T) {
	base := `from django.db import models
class BaseModel(models.Model):
    created = models.DateTimeField()
    class Meta:
        abstract = True
`
	box := `from core.models import BaseModel
class Box(BaseModel):
    code = models.CharField(max_length=10)
`
	ms, _ := detectModels("w", []File{{Path: "core/models.py", Content: base}, {Path: "boxes/models/box.py", Content: box}})
	b := modelByName(t, ms, "Box")
	if b.Framework != "django" || b.Table != "boxes_box" {
		t.Errorf("project base resolves in-repo first; models/ package app label: %+v", b)
	}
}

func TestTypeORMPrismaGORM(t *testing.T) {
	ts := `import { Entity, Column, PrimaryGeneratedColumn, ManyToOne, OneToMany } from "typeorm";

@Entity("orders")
export class Order {
  @PrimaryGeneratedColumn()
  id: number;

  @Column({ nullable: true, unique: true })
  number: string;

  @ManyToOne(() => Customer, (c) => c.orders)
  customer: Customer;
}

@Entity()
export class Customer {
  @PrimaryGeneratedColumn() id: number;
  @OneToMany(() => Order, (o) => o.customer)
  orders: Order[];
}
`
	prisma := `model User {
  id    Int     @id @default(autoincrement())
  email String  @unique
  posts Post[]
  @@map("users")
}

model Post {
  id       Int   @id
  title    String?
  author   User  @relation(fields: [authorId], references: [id])
  authorId Int
}

enum Role {
  USER
}
`
	gorm := "package shop\n\nimport \"gorm.io/gorm\"\n\ntype Company struct {\n\tgorm.Model\n\tName string `gorm:\"size:100;not null;uniqueIndex\"`\n\tEmployees []Employee\n}\n\ntype Employee struct {\n\tID        uint\n\tCompanyID uint\n\tCompany   Company\n\tEmail     *string `gorm:\"column:mail\"`\n}\n\nfunc (Employee) TableName() string { return \"staff\" }\n\ntype NotAModel struct {\n\tX int\n}\n"
	ms, _ := detectModels("x", []File{{Path: "src/entities.ts", Content: ts}, {Path: "prisma/schema.prisma", Content: prisma}, {Path: "shop/models.go", Content: gorm}})
	o := modelByName(t, ms, "Order")
	if o.Table != "orders" || o.Framework != "typeorm" {
		t.Errorf("typeorm: %+v", o)
	}
	if f := fieldByName(t, o, "number"); !f.Null || !f.Unique {
		t.Errorf("column options: %+v", f)
	}
	if f := fieldByName(t, o, "id"); !f.PK {
		t.Errorf("primary: %+v", f)
	}
	if r := relByField(t, o, "customer"); r.Kind != "fk" || r.Target != "Customer" || r.Reverse != "orders" {
		t.Errorf("many-to-one: %+v", r)
	}
	if c := modelByName(t, ms, "Customer"); c.Table != "customer" || len(c.Fields) != 1 || relByField(t, c, "orders").Kind != "has_many" {
		t.Errorf("same-line decorator / one-to-many: %+v", c)
	}
	u := modelByName(t, ms, "User")
	if u.Table != "users" || !fieldByName(t, u, "email").Unique || relByField(t, u, "posts").Kind != "has_many" {
		t.Errorf("prisma user: %+v", u)
	}
	p := modelByName(t, ms, "Post")
	if r := relByField(t, p, "authorId"); r.Kind != "fk" || r.Target != "User" {
		t.Errorf("prisma relation: %+v", r)
	}
	if !fieldByName(t, p, "title").Null {
		t.Errorf("prisma optional")
	}
	for _, m := range ms {
		if m.Name == "Role" || m.Name == "NotAModel" {
			t.Errorf("not a model: %s", m.Name)
		}
	}
	co := modelByName(t, ms, "Company")
	if co.Table != "companies" || !fieldByName(t, co, "ID").PK || !fieldByName(t, co, "Name").Unique || fieldByName(t, co, "Name").Null {
		t.Errorf("gorm company: %+v", co)
	}
	if r := relByField(t, co, "Employees"); r.Kind != "has_many" || r.Target != "Employee" {
		t.Errorf("gorm has_many: %+v", r)
	}
	e := modelByName(t, ms, "Employee")
	if e.Table != "staff" || relByField(t, e, "Company").Kind != "fk" || fieldByName(t, e, "Email").Column != "mail" || !fieldByName(t, e, "Email").Null {
		t.Errorf("gorm employee: %+v", e)
	}
}

func TestSQLMigrationsAndProto(t *testing.T) {
	m1 := `-- initial
CREATE TABLE IF NOT EXISTS customers (
    id SERIAL PRIMARY KEY,
    email VARCHAR(200) NOT NULL UNIQUE
);
CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    note TEXT DEFAULT 'none',
    legacy INT
);`
	m2 := `ALTER TABLE orders ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'new', DROP COLUMN legacy;
CREATE INDEX orders_status_idx ON orders (status);
CREATE TABLE scratch (x int);
DROP TABLE scratch;`
	down := `DROP TABLE orders;`
	proto := `syntax = "proto3";
package shop.v1;

message Order {
  string id = 1;
  repeated Line lines = 2;
  map<string, Line> by_sku = 3;
  google.protobuf.Timestamp at = 4;
  message Line {
    string sku = 1;
    int32 qty = 2;
  }
  oneof payment {
    string card = 5;
  }
}

enum Status {
  NEW = 0;
}
`
	ms, _ := detectModels("db", []File{{Path: "migrations/001_init.sql", Content: m1}, {Path: "migrations/002_status.sql", Content: m2},
		{Path: "migrations/002_status.down.sql", Content: down}, {Path: "proto/order.proto", Content: proto}})
	o := modelByName(t, ms, "orders")
	if o.Framework != "sql" || o.Kind != KindTable {
		t.Errorf("sql table: %+v", o)
	}
	for _, f := range o.Fields {
		if f.Name == "legacy" {
			t.Errorf("dropped column kept")
		}
	}
	if f := fieldByName(t, o, "status"); f.Null || f.Default != "'new'" || !f.Index {
		t.Errorf("added column / index: %+v", f)
	}
	if f := fieldByName(t, o, "note"); !f.Null || f.Default != "'none'" {
		t.Errorf("default: %+v", f)
	}
	if r := relByField(t, o, "customer_id"); r.Target != "customers" {
		t.Errorf("references: %+v", r)
	}
	if len(o.Sources) != 1 || o.Sources[0] != "migrations/002_status.sql" {
		t.Errorf("sources: %v", o.Sources)
	}
	if f := fieldByName(t, modelByName(t, ms, "customers"), "email"); f.Null || !f.Unique {
		t.Errorf("customers.email: %+v", f)
	}
	for _, m := range ms {
		if m.Name == "scratch" {
			t.Errorf("dropped table kept")
		}
	}
	po := modelByName(t, ms, "Order")
	if po.Kind != KindWire || po.Package != "shop.v1" || len(po.Fields) != 5 {
		t.Errorf("proto message: %+v", po)
	}
	if r := relByField(t, po, "lines"); r.Kind != "ref_many" || r.Target != "Order.Line" {
		t.Errorf("nested ref: %+v", r)
	}
	if _, ok := map[string]bool{}[""]; ok {
	}
	l := modelByName(t, ms, "Order.Line")
	if len(l.Fields) != 2 {
		t.Errorf("nested message: %+v", l)
	}
	for _, r := range po.Relations {
		if strings.HasPrefix(r.Raw, "google.protobuf") {
			t.Errorf("well-known type as a relation: %+v", r)
		}
	}
}

// dataStore is a files-only store whose symbol table is the spans' names
// qualified by directory (what the indexer would record).
type dataStore struct{ filesStore }

func (d dataStore) SymbolsAt(repo, ref string) map[string]SymbolRef {
	out := map[string]SymbolRef{}
	for _, f := range d.files[repo] {
		dir := f.Path[:strings.LastIndex(f.Path+"/", "/")]
		if i := strings.LastIndex(f.Path, "/"); i >= 0 {
			dir = f.Path[:i]
		} else {
			dir = ""
		}
		for _, s := range f.Symbols {
			q := s.Name
			if dir != "" {
				q = dir + "." + s.Name
			}
			out[q] = SymbolRef{}
		}
	}
	return out
}

func TestModelDetailReadersWritersRoutes(t *testing.T) {
	views := `from shop.models import Order, Customer
from shop.serializers import OrderSerializer

class OrderViewSet(viewsets.ModelViewSet):
    serializer_class = OrderSerializer
    queryset = Order.objects.all()

def close_order(request, pk):
    order = get_object_or_404(Order, pk=pk)
    order.save()
    Order.objects.filter(pk=pk).update(
        number="x",
    )

def report(customer):
    return customer.orders.filter(number__startswith="A").count()

def purge():
    with connection.cursor() as c:
        c.execute("DELETE FROM orders WHERE number IS NULL")
`
	urls := `from rest_framework import routers
router = routers.DefaultRouter()
router.register(r"orders", OrderViewSet)
urlpatterns = [path("api/", include(router.urls))]
`
	signals := `@receiver(post_save, sender=Order)
def on_order(sender, instance, **kw):
    pass
`
	other := `def nightly():
    rows = db.query("SELECT id FROM orders WHERE created < now()")
    # from orders import nothing
`
	viewSpans := []SymbolSpan{{Name: "OrderViewSet", StartLine: 4, EndLine: 6}, {Name: "close_order", StartLine: 8, EndLine: 13},
		{Name: "report", StartLine: 15, EndLine: 16}, {Name: "purge", StartLine: 18, EndLine: 20}}
	s := dataStore{filesStore{files: map[string][]File{
		"shop": {{Path: "shop/models.py", Content: djangoModels}, {Path: "shop/serializers.py", Content: drfSerializers},
			{Path: "shop/views.py", Content: views, Symbols: viewSpans}, {Path: "shop/urls.py", Content: urls},
			{Path: "shop/signals.py", Content: signals, Symbols: []SymbolSpan{{Name: "on_order", StartLine: 2, EndLine: 3}}}},
		"jobs": {{Path: "jobs/nightly.py", Content: other, Symbols: []SymbolSpan{{Name: "nightly", StartLine: 1, EndLine: 3}}}},
	}}}
	r := Models(s, FleetRepo, "v1", "Order", ModelsOptions{})
	if len(r.Details) != 1 {
		t.Fatalf("details: %+v", r)
	}
	d := r.Details[0]
	ops := func(xs []DataAccess) map[string]DataAccess {
		out := map[string]DataAccess{}
		for _, x := range xs {
			out[x.Path+":"+itoaT(x.Line)] = x
		}
		return out
	}
	w, rd := ops(d.Writers), ops(d.Readers)
	if x, ok := w["shop/views.py:11"]; !ok || x.Op != "update" || x.Handle != "shop.close_order" {
		t.Errorf("chained update is a write in close_order: %+v (writers %+v)", x, d.Writers)
	}
	if x, ok := w["shop/views.py:10"]; !ok || x.Op != "save" || x.Via != "instance-name" {
		t.Errorf("instance save: %+v", x)
	}
	if x, ok := w["shop/views.py:20"]; !ok || x.Op != "DELETE" || x.Via != "raw-sql" {
		t.Errorf("raw delete: %+v", x)
	}
	if x, ok := rd["shop/views.py:6"]; !ok || x.Op != "all" || x.Handle != "shop.OrderViewSet" {
		t.Errorf("queryset read: %+v", x)
	}
	if x, ok := rd["shop/views.py:9"]; !ok || x.Op != "get_object_or_404" {
		t.Errorf("get_object_or_404: %+v", x)
	}
	if x, ok := rd["shop/views.py:16"]; !ok || x.Via != "related-manager" {
		t.Errorf("related manager: %+v", x)
	}
	if x, ok := rd["jobs/nightly.py:2"]; !ok || x.Repo != "jobs" || x.Op != "SELECT" && x.Op != "FROM" {
		t.Errorf("fleet-wide raw SQL read: %+v (readers %+v)", x, d.Readers)
	}
	if _, ok := rd["jobs/nightly.py:3"]; ok {
		t.Errorf("a comment 'from orders import' is not SQL")
	}
	if len(d.Hooks) != 1 || d.Hooks[0].Path != "shop/signals.py" {
		t.Errorf("signal hook: %+v", d.Hooks)
	}
	if len(d.Serializers) != 1 || d.Serializers[0].Name != "OrderSerializer" {
		t.Errorf("serializers: %+v", d.Serializers)
	}
	found := false
	for _, rt := range d.Routes {
		if rt.Pattern == "api/orders/" {
			found = true
		}
	}
	if !found {
		t.Errorf("route via the viewset: %+v", d.Routes)
	}
	in := false
	for _, x := range modelDetailIncoming(s, "Customer") {
		in = in || x.From == "Order" && x.Field == "customer"
	}
	if !in {
		t.Errorf("Customer's incoming fk from Order missing")
	}
	// A tight budget truncates with a hint instead of silently cutting.
	small := Models(s, FleetRepo, "v1", "Order", ModelsOptions{Budget: 150})
	if !small.Truncated || small.Next == "" || small.Omitted == 0 {
		t.Errorf("budget truncation: %+v", small)
	}
}

func modelDetailIncoming(s Store, name string) []DataRelation {
	r := Models(s, FleetRepo, "v1", name, ModelsOptions{})
	if len(r.Details) == 0 {
		return nil
	}
	return r.Details[0].Incoming
}

func itoaT(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(n)) }

func fmtInt(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if s == "" {
		return "0"
	}
	return s
}

func TestModelsListRankedAndBudgeted(t *testing.T) {
	s := dataStore{filesStore{files: map[string][]File{"shop": {{Path: "shop/models.py", Content: djangoModels}, {Path: "shop/serializers.py", Content: drfSerializers}}}}}
	r := Models(s, FleetRepo, "v1", "", ModelsOptions{})
	if len(r.Models) == 0 || r.Models[0].Kind != KindTable {
		t.Fatalf("tables first: %+v", r.Models)
	}
	if r.Models[0].Name != "Order" {
		t.Errorf("most connected first, got %s", r.Models[0].Name)
	}
	last := r.Models[len(r.Models)-1]
	if last.Kind != KindSchema {
		t.Errorf("schemas last: %+v", last)
	}
	k := Models(s, FleetRepo, "v1", "", ModelsOptions{Kind: KindSchema})
	if len(k.Models) != 1 || k.Models[0].Name != "UploadSerializer" {
		t.Errorf("kind filter: %+v", k.Models)
	}
	tiny := Models(s, FleetRepo, "v1", "", ModelsOptions{Budget: 70})
	if !tiny.Truncated || tiny.Omitted == 0 || !strings.Contains(tiny.Next, "--budget") {
		t.Errorf("list truncation: %+v", tiny)
	}
	miss := Models(s, FleetRepo, "v1", "Ordr", ModelsOptions{})
	if len(miss.Details) != 0 || miss.Note == "" {
		t.Errorf("miss: %+v", miss)
	}
	if byTable := Models(s, FleetRepo, "v1", "shop_customer", ModelsOptions{}); len(byTable.Details) != 1 || byTable.Details[0].Model.Name != "Customer" {
		t.Errorf("lookup by table name: %+v", byTable.Details)
	}
}

func TestOtherFrameworkAccess(t *testing.T) {
	sa := `class User(Base):
    __tablename__ = "users"
    id = Column(Integer, primary_key=True)
`
	svc := `def add_user(session, name):
    u = User(name=name)
    session.add(u)
    return session.query(User).filter_by(name=name).first()

def wipe(session):
    session.execute(delete(User).where(User.id > 0))
`
	ts := `@Entity("orders")
export class Order {
  @PrimaryGeneratedColumn() id: number;
}
`
	tsSvc := `export class OrderService {
  constructor(@InjectRepository(Order) private readonly orders: Repository<Order>, private prisma: PrismaClient) {}
  list() { return this.orders.find({ where: {} }); }
  make(o) { return this.orders.save(o); }
  users() { return this.prisma.user.findMany(); }
}
`
	prisma := "model User {\n  id Int @id\n}\n"
	gormSrc := "package shop\n\nimport \"gorm.io/gorm\"\n\ntype Item struct {\n\tgorm.Model\n\tSKU string `gorm:\"unique\"`\n}\n"
	gormSvc := "package shop\n\nfunc Save(db *gorm.DB, it *Item) error {\n\treturn db.Create(it).Error\n}\n\nfunc All(db *gorm.DB) (out []Item) {\n\tdb.Where(\"sku <> ''\").Find(&out)\n\treturn\n}\n"
	s := dataStore{filesStore{files: map[string][]File{
		"py": {{Path: "app/db.py", Content: sa}, {Path: "app/svc.py", Content: svc, Symbols: []SymbolSpan{{Name: "add_user", StartLine: 1, EndLine: 4}, {Name: "wipe", StartLine: 6, EndLine: 7}}}},
		"ts": {{Path: "src/order.entity.ts", Content: ts}, {Path: "src/order.service.ts", Content: tsSvc}, {Path: "prisma/schema.prisma", Content: prisma}},
		"go": {{Path: "shop/item.go", Content: gormSrc}, {Path: "shop/svc.go", Content: gormSvc}},
	}}}
	check := func(repo, model string, wantW, wantR []string) {
		t.Helper()
		r := Models(s, repo, "v1", model, ModelsOptions{})
		if len(r.Details) == 0 {
			t.Fatalf("%s: no detail (%s)", model, r.Note)
		}
		d := r.Details[0]
		got := func(xs []DataAccess) []string {
			var out []string
			for _, x := range xs {
				out = append(out, x.Op)
			}
			return out
		}
		for _, w := range wantW {
			if !containsStr(got(d.Writers), w) {
				t.Errorf("%s writers %v missing %s", model, got(d.Writers), w)
			}
		}
		for _, w := range wantR {
			if !containsStr(got(d.Readers), w) {
				t.Errorf("%s readers %v missing %s", model, got(d.Readers), w)
			}
		}
	}
	check("py", "User", []string{"add", "delete"}, []string{"filter_by"})
	check("ts", "Order", []string{"save"}, []string{"find"})
	check("ts", "User", nil, []string{"findMany"})
	check("go", "Item", []string{"Create"}, []string{"Find"})
}
