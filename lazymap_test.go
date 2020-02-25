package lazymap_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/waylen888/lazymap"
)

func Test_Zero(t *testing.T) {
	var m lazymap.Map
	m.LoadOrCtor(nil, nil, nil)
	m.Delete("_")
}

func Test_NilCtor(t *testing.T) {
	m := lazymap.New(time.Second)
	o, err := m.LoadOrCtor(context.Background(), "_", nil)
	if err != lazymap.ErrCtorNotProvided {
		t.Fatalf("%v is not ErrCtorNotProvided", err)
	}
	if o != nil {
		t.Fatal("o is not nil")
	}
}

func Test_EndOfLifetime(t *testing.T) {
	m := lazymap.New(time.Second)
	m.OnDelete = func(_, ch interface{}) {
		ch.(chan struct{}) <- struct{}{}
	}
	ch, _ := m.LoadOrCtor(context.Background(), "_", func(ctx context.Context, _ interface{}) (interface{}, error) {
		return make(chan struct{}, 1), nil
	})
	select {
	case <-time.After(time.Second * 5):
		t.Fatal("OnDelete not be called")
	case <-ch.(chan struct{}):
	}
}

func Test_DeleteValue(t *testing.T) {
	m := lazymap.New(0)
	val, _ := m.LoadOrCtor(context.Background(), "_", func(ctx context.Context, _ interface{}) (interface{}, error) {
		return "value1", nil
	})
	if val != "value1" {
		t.Fatalf("Load value %v not matched", val)
	}

	m.Delete("_")

	val, _ = m.LoadOrCtor(context.Background(), "_", func(ctx context.Context, _ interface{}) (interface{}, error) {
		return "value2", nil
	})
	if val != "value2" {
		t.Fatalf("Load value %v not matched", val)
	}
}

func Test_LoadError(t *testing.T) {
	m := lazymap.New(0)
	val, err := m.LoadOrCtor(context.Background(), "_", func(ctx context.Context, _ interface{}) (interface{}, error) {
		return nil, errors.New("some error")
	})
	if err.Error() != "some error" {
		t.Fatalf("unexpected error %v", err)
	}

	val, err = m.LoadOrCtor(context.Background(), "_", func(ctx context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	})

	if val != "ok" {
		t.Fatalf("unexpected val %v", val)
	}
}

func Test_MultipleLoad(t *testing.T) {
	m := lazymap.New(time.Second * 5)
	ctor := func(ctx context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}
	for i := 0; i < 10; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			val, err := m.LoadOrCtor(context.Background(), "_", ctor)
			if err != nil {
				t.Fatal("LoadOrCtor error not nil")
			}
			if val != "ok" {
				t.Fatalf("LoadOrCtor unexpected val %v", val)
			}
		})
	}
	t.Parallel()
}

func Test_DeadlineExceeded(t *testing.T) {
	m := lazymap.New(time.Millisecond * 100)
	ctorCnt := 0
	ctor := func(ctx context.Context, _ interface{}) (interface{}, error) {
		ctorCnt++
		return "ok", nil
	}
	for i := 0; i < 2; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := m.LoadOrCtor(ctx, "_", ctor)
			if err != nil {
				t.Fatal("LoadOrCtor error not nil")
			}
		})
		time.Sleep(time.Second)
	}
	t.Parallel()
	if ctorCnt != 2 {
		t.Fatalf("i is %d", ctorCnt)
	}
}
