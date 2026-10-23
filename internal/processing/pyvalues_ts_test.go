//go:build treesitter

package processing

import "testing"

// A parameter annotated with a callable — Callable[...] itself, a PEP 695
// or plain alias of one, a Protocol's __call__ — gives what calling it
// returns; an awaited call what the coroutine returns.
func TestPythonCallableTypedParams(t *testing.T) {
	files := map[string]string{
		"shop/builders.py": `from collections.abc import Awaitable, Callable
from typing import Protocol

type BuilderFactory = Callable[[], "Builder"]
AsyncBuilderFactory = Callable[..., Awaitable[Builder]]

class Builder:
    def commit(self):
        pass

class Other:
    def commit(self):
        pass

class BuilderMaker(Protocol):
    async def __call__(self, size: int = 1) -> Builder: ...
`,
		"shop/tests/test_build.py": `from collections.abc import Callable
from shop.builders import AsyncBuilderFactory, Builder, BuilderFactory, BuilderMaker

def test_alias(make: BuilderFactory):
    b = make()
    b.commit()

async def test_async_alias(make: AsyncBuilderFactory):
    b = await make()
    b.commit()

async def test_protocol(make: BuilderMaker):
    b = await make(size=2)
    b.commit()

def test_callable(make: Callable[[], Builder]):
    make().commit()
`,
	}
	edges := indexAndEdges(t, files)
	for _, fn := range []string{"test_alias", "test_async_alias", "test_protocol", "test_callable"} {
		e := edges["shop/tests.test_build."+fn]
		wantMethod(t, e, "shop.builders.Builder.commit", MethodTyped)
		if _, bad := e["shop.builders.Other.commit"]; bad {
			t.Errorf("%s: commit pinned on Other", fn)
		}
	}
}

// A fixture another module defines, brought in through a conftest's
// pytest_plugins, that hands the test a function it defines: calling the
// parameter gives what that function returns.
func TestPythonFixtureFactoryThroughPlugins(t *testing.T) {
	files := map[string]string{
		"shop/fixtures/builders.py": `import pytest

class Builder:
    def commit(self):
        pass

@pytest.fixture
def builder(session):
    def _new() -> Builder:
        return Builder(session)
    return _new
`,
		"shop/conftest.py": `pytest_plugins = ["shop.fixtures.builders"]
`,
		"shop/tests/test_orders.py": `def test_commit(builder):
    b = builder()
    b.commit()
`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["shop/tests.test_orders.test_commit"], "shop/fixtures.builders.Builder.commit", MethodTyped)
}

// A classmethod returning cls(...) gives an instance of the class it is
// called on — exactly that class when it is named; a method returning self
// the instance it is called on.
func TestPythonClassmethodAndSelfReturns(t *testing.T) {
	files := map[string]string{
		"auth/data.py": `class User:
    @classmethod
    def lookup(cls, name):
        return cls(name)

    def rename(self, name):
        self.name = name
        return self

    def save(self):
        pass

class AdminUser(User):
    def save(self):
        pass
`,
		"auth/views.py": `from auth.data import User

def update(name):
    u = User.lookup(name)
    v = u.rename("x")
    v.save()

def chained(name):
    User.lookup(name).save()
`,
	}
	edges := indexAndEdges(t, files)
	up := edges["auth.views.update"]
	wantMethod(t, up, "auth.data.User.rename", MethodTyped)
	wantMethod(t, up, "auth.data.User.save", MethodTyped)
	if _, bad := up["auth.data.AdminUser.save"]; bad {
		t.Errorf("User.lookup(...) reaches a subclass's save: %v", up)
	}
	ch := edges["auth.views.chained"]
	wantMethod(t, ch, "auth.data.User.save", MethodTyped)
	if _, bad := ch["auth.data.AdminUser.save"]; bad {
		t.Errorf("User.lookup(...).save() reaches a subclass's save: %v", ch)
	}
}

// A module-level instance is its class's: used in its own module, imported
// by name, or reached through the module.
func TestPythonModuleLevelInstances(t *testing.T) {
	files := map[string]string{
		"api/client.py": `class Client:
    def send(self, msg):
        pass

client = Client()

def ping():
    client.send("ping")
`,
		"api/other.py": `class Mailer:
    def send(self, msg):
        pass
`,
		"app/jobs.py": `from api.client import client
import api.client
from api import client as client_mod

def run():
    client.send("a")

def run_attr():
    api.client.client.send("b")

def run_module():
    client_mod.client.send("c")

def shadowed(client):
    client.send("d")
`,
	}
	edges := indexAndEdges(t, files)
	for _, fn := range []string{"api.client.ping", "app.jobs.run", "app.jobs.run_attr", "app.jobs.run_module"} {
		e := edges[fn]
		wantMethod(t, e, "api.client.Client.send", MethodTyped)
		if _, bad := e["api.other.Mailer.send"]; bad {
			t.Errorf("%s: send pinned on Mailer", fn)
		}
	}
	if m := edges["app.jobs.shadowed"]["api.client.Client.send"]; m == MethodTyped {
		t.Errorf("a parameter named like the module's instance typed as it")
	}
}

// A field set from an annotated parameter has the parameter's class; an
// annotated self in a plain function (a bound task) is that class.
func TestPythonFieldFromParamAndAnnotatedSelf(t *testing.T) {
	files := map[string]string{
		"shop/repo.py": `class Repo:
    def save(self):
        pass

class Job:
    def run(self):
        pass
`,
		"shop/service.py": `from shop.repo import Job, Repo

class Service:
    def __init__(self, *, repo: Repo):
        self._repo = repo

    def store(self):
        self._repo.save()

def task(self: Job, data):
    self.run()
`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["shop.service.Service.store"], "shop.repo.Repo.save", MethodTyped)
	wantMethod(t, edges["shop.service.task"], "shop.repo.Job.run", MethodTyped)
}

// A yield fixture annotated Iterator[X] hands the test an X; a function
// returning a tuple gives each unpacked name its item.
func TestPythonIteratorFixtureAndTupleUnpack(t *testing.T) {
	files := map[string]string{
		"store/db.py": `class Db:
    def scan(self):
        pass

class Line:
    def total(self):
        pass

class Order:
    def total(self):
        pass

def make():
    o = Order()
    return o, Line()
`,
		"store/tests/conftest.py": `from collections.abc import Iterator
import pytest
from store.db import Db

@pytest.fixture
def db() -> Iterator[Db]:
    d = connect()
    yield d
`,
		"store/tests/test_db.py": `from store.db import make

def test_scan(db):
    db.scan()

def test_unpack():
    order, line = make()
    line.total()
`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["store/tests.test_db.test_scan"], "store.db.Db.scan", MethodTyped)
	up := edges["store/tests.test_db.test_unpack"]
	wantMethod(t, up, "store.db.Line.total", MethodTyped)
	if _, bad := up["store.db.Order.total"]; bad {
		t.Errorf("line.total() pinned on Order: %v", up)
	}
}

// Calling a parameter is calling the caller's value, never the module's
// function of that name; a bare call of a name only a nested function binds
// is still the module's function in the enclosing one.
func TestPythonParamCallShadowsModuleFunction(t *testing.T) {
	files := map[string]string{
		"shop/orders.py": `class Order:
    def save(self):
        pass

def factory():
    return Order()

def build(factory):
    x = factory()
    x.save()

def outer():
    def inner(factory):
        return factory
    factory()
`,
	}
	edges := indexAndEdges(t, files)
	if m := edges["shop.orders.build"]["shop.orders.Order.save"]; m == MethodTyped {
		t.Errorf("a parameter's call typed as the module function's return")
	}
	wantMethod(t, edges["shop.orders.outer"], "shop.orders.factory", MethodResolved)
}

// A union annotation reaches the method of each class it names; a class a
// class attribute holds is constructed by calling it.
func TestPythonUnionAndClassHoldingField(t *testing.T) {
	files := map[string]string{
		"shop/services.py": `from typing import Annotated

class ReadService:
    def exists(self):
        pass

class WriteService:
    def exists(self):
        pass

class OrderFactory:
    pass

class Factories:
    order = OrderFactory

def check(s: ReadService | WriteService, t: Annotated[ReadService, "dep"]):
    s.exists()
    t.exists()

def make(f: Factories):
    f.order()
`,
	}
	edges := indexAndEdges(t, files)
	ch := edges["shop.services.check"]
	wantMethod(t, ch, "shop.services.ReadService.exists", MethodTyped)
	wantMethod(t, ch, "shop.services.WriteService.exists", MethodTyped)
	if _, ok := edges["shop.services.make"]["shop.services.OrderFactory"]; !ok {
		t.Errorf("f.order() does not construct OrderFactory: %v", edges["shop.services.make"])
	}
}

// A script importing a module beside it inside a package finds it; a
// standard-library name is never a sibling module.
func TestPythonScriptSiblingImports(t *testing.T) {
	files := map[string]string{
		"tools/__init__.py": ``,
		"tools/helper.py": `def tool():
    pass
`,
		"tools/json.py": `def dumps(x):
    pass
`,
		"tools/main.py": `from helper import tool
import json

def run():
    tool()
    json.dumps({})
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["tools.main.run"]
	wantMethod(t, run, "tools.helper.tool", MethodResolved)
	if _, bad := run["tools.json.dumps"]; bad {
		t.Errorf("import json resolved to a sibling json.py: %v", run)
	}
}

// A property's value is what its getter returns.
func TestPythonPropertyValue(t *testing.T) {
	files := map[string]string{
		"docs/models.py": `from functools import cached_property

class Document:
    def path(self):
        pass

class Task:
    @property
    def document(self) -> Document:
        return load()

    @cached_property
    def backup(self) -> Document:
        return load()

    def run(self):
        self.document.path()
        self.backup.path()
`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["docs.models.Task.run"], "docs.models.Document.path", MethodTyped)
}
