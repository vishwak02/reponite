//go:build treesitter

package processing

import "testing"

// A call through an abstract method whose class the repo implements once
// runs that implementation: the program text decides it, so it is the
// resolved target, and the declaration stays an edge of its own. Several
// implementations stay a list; a test's double never stands in for a
// production caller's target, yet a test caller sees it.
func TestPythonAbstractSingleImplementation(t *testing.T) {
	files := map[string]string{
		"shop/store.py": `from abc import ABC, abstractmethod
from typing import Protocol
from lib.base import LibBase


class Store(ABC):
    @abstractmethod
    def load(self, k):
        ...

    @abstractmethod
    def save(self, k):
        ...


class SqlStore(Store):
    def load(self, k):
        return k

    def save(self, k):
        return k


class FileStore(Store):
    def save(self, k):
        return k


class Cache(ABC):
    @abstractmethod
    def get(self, k):
        ...


class GetMixin:
    def get(self, k):
        return k


class MemCache(GetMixin, Cache):
    pass


class Feed(ABC):
    @abstractmethod
    def pull(self):
        ...


class LibFeed(LibBase, Feed):
    pass


class HttpFeed(Feed):
    def pull(self):
        return 1


class Sink(Protocol):
    def put(self, v): ...


class ListSink(Sink):
    def put(self, v):
        return v


class Clock(ABC):
    @abstractmethod
    def now(self):
        ...


class FakeClock(Clock):
    def now(self):
        return 0


class WallClock(Clock):
    def now(self):
        return 1


class Model:
    def update(self):
        raise NotImplementedError

    def apply(self):
        self.update()


class Disk(Model):
    def update(self):
        return 1


class Secret(Model):
    def update(self):
        raise NotImplementedError("a secret is never updated")
`,
		"shop/use.py": `from shop.store import Store, Cache, Feed, Sink, Clock


def run(s: Store, c: Cache, f: Feed, k: Sink, w: Clock):
    s.load(1)
    s.save(1)
    c.get(1)
    f.pull()
    k.put(1)
    w.now()
`,
		"tests/fakes.py": `from shop.store import Store


class MemStore(Store):
    def load(self, k):
        return None

    def save(self, k):
        return None
`,
		"tests/test_use.py": `from shop.store import Store


def test_load(s: Store):
    s.load(1)
`,
	}
	edges := indexAndEdges(t, files)
	use := edges["shop.use.run"]
	wantMethod(t, use, "shop.store.SqlStore.load", MethodAbstractSingle)
	wantMethod(t, use, "shop.store.Store.load", MethodAbstractDecl)
	if _, ok := use["tests.fakes.MemStore.load"]; ok {
		t.Errorf("a production call never runs a test's double: %v", use)
	}
	// two classes implement save: the running program picks
	wantMethod(t, use, "shop.store.SqlStore.save", MethodAbstractImpl)
	wantMethod(t, use, "shop.store.FileStore.save", MethodAbstractImpl)
	wantMethod(t, use, "shop.store.Store.save", MethodAbstractDecl)
	// a mixin ahead of the abstract base implements it
	wantMethod(t, use, "shop.store.GetMixin.get", MethodAbstractSingle)
	wantMethod(t, use, "shop.store.Cache.get", MethodAbstractDecl)
	// a library base ahead of the abstract one may implement it: undecided
	if m := use["shop.store.HttpFeed.pull"]; m == MethodAbstractSingle {
		t.Errorf("a subclass whose library base comes first is not decided: %v", use)
	}
	// a Protocol is implemented without naming it
	if m := use["shop.store.ListSink.put"]; m == MethodAbstractSingle {
		t.Errorf("a Protocol's one named implementation is not its only one: %v", use)
	}
	// a Fake class beside production code may run in production
	if m := use["shop.store.WallClock.now"]; m == MethodAbstractSingle {
		t.Errorf("a production file's Fake class is a possible target: %v", use)
	}
	// an override that only raises NotImplementedError runs (and raises):
	// two implementations
	apply := edges["shop.store.Model.apply"]
	wantMethod(t, apply, "shop.store.Disk.update", MethodAbstractImpl)
	wantMethod(t, apply, "shop.store.Secret.update", MethodAbstractImpl)
	wantMethod(t, apply, "shop.store.Model.update", MethodAbstractDecl)
	// the test caller sees its own double too
	tl := edges["tests.test_use.test_load"]
	wantMethod(t, tl, "tests.fakes.MemStore.load", MethodAbstractImpl)
	wantMethod(t, tl, "shop.store.SqlStore.load", MethodAbstractImpl)
}

// A value the code visibly makes of one class runs that class's method,
// whatever class its annotation names: an annotated local bound once, a
// field annotated with the base and set to a subclass's instance, a
// function annotated to return the base whose every return constructs the
// same subclass. The annotation's method stays a declaration edge.
func TestPythonVisibleConstruction(t *testing.T) {
	files := map[string]string{
		"shop/notify.py": `from abc import ABC, abstractmethod


class Sender(ABC):
    @abstractmethod
    def send(self, m):
        ...


class Mail(Sender):
    def send(self, m):
        return m


class Sms(Sender):
    def send(self, m):
        return m


def make_sender() -> Sender:
    return Sms()


def pick_sender(fast) -> Sender:
    if fast:
        return Sms()
    return Mail()


class Hub:
    def __init__(self):
        self.out: Sender = Mail()

    def push(self, m):
        self.out.send(m)


def local_once(m):
    s: Sender = Mail()
    s.send(m)


def local_twice(m, fast):
    s: Sender = Mail()
    if fast:
        s = Sms()
    s.send(m)


def factory(m):
    make_sender().send(m)


def factory_mixed(m):
    pick_sender(True).send(m)
`,
	}
	edges := indexAndEdges(t, files)
	once := edges["shop.notify.local_once"]
	wantMethod(t, once, "shop.notify.Mail.send", MethodTyped)
	wantMethod(t, once, "shop.notify.Sender.send", MethodAbstractDecl)
	if _, ok := once["shop.notify.Sms.send"]; ok {
		t.Errorf("a local made of Mail never runs Sms.send: %v", once)
	}
	twice := edges["shop.notify.local_twice"]
	wantMethod(t, twice, "shop.notify.Mail.send", MethodAbstractImpl)
	wantMethod(t, twice, "shop.notify.Sms.send", MethodAbstractImpl)
	push := edges["shop.notify.Hub.push"]
	wantMethod(t, push, "shop.notify.Mail.send", MethodTyped)
	wantMethod(t, push, "shop.notify.Sender.send", MethodAbstractDecl)
	if _, ok := push["shop.notify.Sms.send"]; ok {
		t.Errorf("a field made of Mail never runs Sms.send: %v", push)
	}
	fac := edges["shop.notify.factory"]
	wantMethod(t, fac, "shop.notify.Sms.send", MethodTyped)
	wantMethod(t, fac, "shop.notify.Sender.send", MethodAbstractDecl)
	if _, ok := fac["shop.notify.Mail.send"]; ok {
		t.Errorf("a factory whose every return is Sms(): %v", fac)
	}
	mixed := edges["shop.notify.factory_mixed"]
	wantMethod(t, mixed, "shop.notify.Mail.send", MethodAbstractImpl)
	wantMethod(t, mixed, "shop.notify.Sms.send", MethodAbstractImpl)
}