package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
    char*   data;
    int32_t len;
    int32_t status;
    char*   err;
} tako_buf_t;

typedef struct {
    const char* method;
    const char* path;
    const char* raw_query;
    const char* body;
    int32_t     body_len;
    uint64_t    ctx_id;
} tako_http_req_t;

// Callback function pointers from Go into C/C++
typedef tako_buf_t (*tako_http_cb)(tako_http_req_t req, void* user_data);
typedef tako_buf_t (*tako_async_cb)(uint64_t ctx_id, const char* payload, int32_t payload_len, void* user_data);
typedef tako_buf_t (*tako_cron_cb)(uint64_t ctx_id, void* user_data);
typedef tako_buf_t (*tako_tx_cb)(uint64_t tx_id, void* user_data);

// Trampolines to invoke function pointers from cgo
static inline tako_buf_t invoke_http_cb(tako_http_cb cb, tako_http_req_t req, void* user_data) {
    return cb(req, user_data);
}

static inline tako_buf_t invoke_async_cb(tako_async_cb cb, uint64_t ctx_id, const char* payload, int32_t payload_len, void* user_data) {
    return cb(ctx_id, payload, payload_len, user_data);
}

static inline tako_buf_t invoke_cron_cb(tako_cron_cb cb, uint64_t ctx_id, void* user_data) {
    return cb(ctx_id, user_data);
}

static inline tako_buf_t invoke_tx_cb(tako_tx_cb cb, uint64_t tx_id, void* user_data) {
    return cb(tx_id, user_data);
}
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/boot"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
	"github.com/gsandoval/tako/pkg/backends/local"
	"github.com/gsandoval/tako/pkg/runtime"
)

// Global registries for C-ABI context and transaction handles
var (
	ctxCounter uint64
	ctxRegistry sync.Map // uint64 -> context.Context (or *http.Request)

	txCounter  uint64
	txRegistry sync.Map // uint64 -> backends.EntityTxOps

	appCounter uint64
	appRegistry sync.Map // uint64 -> *testAppInstance

	// Entity schema metadata registered from cfire
	entitySchemas sync.Map // string (kind) -> *entitySchema
)

type entitySchema struct {
	idField    string
	indexes    []string
	composites []string
}

type testAppInstance struct {
	app        *boot.App
	dir        string
	mux        *http.ServeMux
	handlerMap map[string]http.Handler
}

func registerCtx(ctx context.Context, r *http.Request) uint64 {
	id := atomic.AddUint64(&ctxCounter, 1)
	if r != nil {
		ctxRegistry.Store(id, r)
	} else if ctx != nil {
		ctxRegistry.Store(id, ctx)
	} else {
		ctxRegistry.Store(id, context.Background())
	}
	return id
}

func unregisterCtx(id uint64) {
	ctxRegistry.Delete(id)
}

func getCtx(id uint64) (context.Context, *http.Request) {
	val, ok := ctxRegistry.Load(id)
	if !ok {
		return context.Background(), nil
	}
	if req, ok := val.(*http.Request); ok {
		return req.Context(), req
	}
	if ctx, ok := val.(context.Context); ok {
		return ctx, nil
	}
	return context.Background(), nil
}

// Helpers for buffer allocation
func makeOkBuf(data []byte) C.tako_buf_t {
	if len(data) == 0 {
		return C.tako_buf_t{data: nil, len: 0, status: 0, err: nil}
	}
	cData := C.CBytes(data)
	return C.tako_buf_t{
		data:   (*C.char)(cData),
		len:    C.int32_t(len(data)),
		status: 0,
		err:    nil,
	}
}

func makeErrBuf(status int, errStr string) C.tako_buf_t {
	var cErr *C.char
	if errStr != "" {
		cErr = C.CString(errStr)
	}
	return C.tako_buf_t{
		data:   nil,
		len:    0,
		status: C.int32_t(status),
		err:    cErr,
	}
}

//export tako_c_free
func tako_c_free(buf C.tako_buf_t) {
	if buf.data != nil {
		C.free(unsafe.Pointer(buf.data))
	}
	if buf.err != nil {
		C.free(unsafe.Pointer(buf.err))
	}
}

//export tako_c_boot
func tako_c_boot() {
	runtime.Boot()
}

//export tako_c_set_queue_rate
func tako_c_set_queue_rate(queueName *C.char, rateLimit C.int32_t) {
	name := C.GoString(queueName)
	runtime.SetQueueRate(name, int(rateLimit))
}

//export tako_c_register_roles
func tako_c_register_roles(rolesCsv *C.char) {
	if rolesCsv == nil {
		return
	}
	raw := C.GoString(rolesCsv)
	parts := strings.Split(raw, ",")
	var roles []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			roles = append(roles, p)
		}
	}
	runtime.RegisterRoles(roles...)
}

// Global server mux for tako_c_serve
var (
	globalMux = http.NewServeMux()
)

//export tako_c_mount_json
func tako_c_mount_json(pattern *C.char, cb C.tako_http_cb, userData unsafe.Pointer, requireAuth C.int32_t, policyName *C.char, cacheSec C.int32_t) {
	pat := C.GoString(pattern)
	var pol string
	if policyName != nil {
		pol = C.GoString(policyName)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID := registerCtx(r.Context(), r)
		defer unregisterCtx(ctxID)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		cMethod := C.CString(r.Method)
		defer C.free(unsafe.Pointer(cMethod))
		cPath := C.CString(r.URL.Path)
		defer C.free(unsafe.Pointer(cPath))
		cRawQuery := C.CString(r.URL.RawQuery)
		defer C.free(unsafe.Pointer(cRawQuery))

		var cBody *C.char
		if len(bodyBytes) > 0 {
			cBody = (*C.char)(C.CBytes(bodyBytes))
			defer C.free(unsafe.Pointer(cBody))
		}

		cReq := C.tako_http_req_t{
			method:    cMethod,
			path:      cPath,
			raw_query: cRawQuery,
			body:      cBody,
			body_len:  C.int32_t(len(bodyBytes)),
			ctx_id:    C.uint64_t(ctxID),
		}

		res := C.invoke_http_cb(cb, cReq, userData)
		defer tako_c_free(res)

		if res.status >= 400 {
			errMsg := "error"
			if res.err != nil {
				errMsg = C.GoString(res.err)
			}
			http.Error(w, errMsg, int(res.status))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if cacheSec > 0 {
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(cacheSec)))
		}
		if res.status > 0 {
			w.WriteHeader(int(res.status))
		} else {
			w.WriteHeader(http.StatusOK)
		}
		if res.data != nil && res.len > 0 {
			goBytes := C.GoBytes(unsafe.Pointer(res.data), res.len)
			_, _ = w.Write(goBytes)
		}
	})

	_ = requireAuth
	_ = pol
	globalMux.Handle(pat, handler)
}

//export tako_c_register_worker
func tako_c_register_worker(varName *C.char, queueName *C.char, cb C.tako_async_cb, userData unsafe.Pointer) {
	vName := C.GoString(varName)
	_ = vName
	qName := C.GoString(queueName)

	workerFn := func(ctx context.Context, payload []byte) error {
		ctxID := registerCtx(ctx, nil)
		defer unregisterCtx(ctxID)

		var cPayload *C.char
		if len(payload) > 0 {
			cPayload = (*C.char)(C.CBytes(payload))
			defer C.free(unsafe.Pointer(cPayload))
		}

		res := C.invoke_async_cb(cb, C.uint64_t(ctxID), cPayload, C.int32_t(len(payload)), userData)
		defer tako_c_free(res)

		if res.status != 0 {
			if res.err != nil {
				return errors.New(C.GoString(res.err))
			}
			return fmt.Errorf("worker failed with status %d", res.status)
		}
		return nil
	}

	if hooks.Queue != nil {
		if disp, ok := hooks.Queue.(*local.QueueDispatcher); ok {
			disp.Consume(qName, workerFn)
		}
	}
}

//export tako_c_register_cron
func tako_c_register_cron(varName *C.char, schedule *C.char, singleflight C.int32_t, cb C.tako_cron_cb, userData unsafe.Pointer) {
	vName := C.GoString(varName)
	sched := C.GoString(schedule)

	_ = vName
	_ = sched
	_ = singleflight
	_ = cb
	_ = userData
}

//export tako_c_serve
func tako_c_serve(hasAuth C.int32_t, authProvidersCsv *C.char, hasBucket C.int32_t, hasCron C.int32_t, siteDir *C.char, siteOptsJson *C.char) C.int32_t {
	_ = hasAuth
	_ = authProvidersCsv
	_ = hasBucket
	_ = hasCron
	_ = siteDir
	_ = siteOptsJson

	runtime.Serve(globalMux)
	return 0
}

// Context inspection helpers
//export tako_c_req_param
func tako_c_req_param(ctxID C.uint64_t, kind *C.char, name *C.char) C.tako_buf_t {
	_, r := getCtx(uint64(ctxID))
	if r == nil {
		return makeErrBuf(400, "no active http request for context")
	}
	k := C.GoString(kind)
	n := C.GoString(name)

	var val string
	switch k {
	case "path":
		val = r.PathValue(n)
	case "query":
		val = r.URL.Query().Get(n)
	case "header":
		val = r.Header.Get(n)
	case "form":
		val = r.FormValue(n)
	default:
		return makeErrBuf(400, fmt.Sprintf("unknown param kind %q", k))
	}
	return makeOkBuf([]byte(val))
}

//export tako_c_ctx_user_json
func tako_c_ctx_user_json(ctxID C.uint64_t) C.tako_buf_t {
	ctx, r := getCtx(uint64(ctxID))
	var user *tako.User
	if r != nil {
		if uVal := r.Header.Get("X-Tako-Test-User"); uVal != "" {
			var u tako.User
			if err := json.Unmarshal([]byte(uVal), &u); err == nil {
				user = &u
			}
		}
	}
	if user == nil {
		if cVal, ok := ctx.(tako.Ctx); ok {
			if u, exists := cVal.MaybeUser(); exists {
				user = &u
			}
		}
	}
	if user == nil {
		return makeErrBuf(401, "unauthorized")
	}
	data, err := json.Marshal(user)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(data)
}

//export tako_c_ctx_now_unix
func tako_c_ctx_now_unix(ctxID C.uint64_t) C.int64_t {
	_ = ctxID
	return C.int64_t(time.Now().Unix())
}

//export tako_c_ctx_log
func tako_c_ctx_log(ctxID C.uint64_t, level *C.char, msg *C.char, kvsJson *C.char) {
	lvl := C.GoString(level)
	m := C.GoString(msg)
	var kvs []string
	if kvsJson != nil {
		_ = json.Unmarshal([]byte(C.GoString(kvsJson)), &kvs)
	}
	fmt.Fprintf(os.Stderr, "[%s] %s %v\n", lvl, m, kvs)
}

// Entity store operations
//export tako_c_entity_register
func tako_c_entity_register(name *C.char, idField *C.char, indexesCsv *C.char, compositesJson *C.char) {
	kind := C.GoString(name)
	idF := C.GoString(idField)
	var idxs []string
	if indexesCsv != nil {
		raw := C.GoString(indexesCsv)
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				idxs = append(idxs, p)
			}
		}
	}
	var comps []string
	if compositesJson != nil {
		_ = json.Unmarshal([]byte(C.GoString(compositesJson)), &comps)
	}
	entitySchemas.Store(kind, &entitySchema{
		idField:    idF,
		indexes:    idxs,
		composites: comps,
	})
}

func getIndexesForKind(kind string) []string {
	if val, ok := entitySchemas.Load(kind); ok {
		return val.(*entitySchema).indexes
	}
	return nil
}

//export tako_c_entity_get
func tako_c_entity_get(ctxID C.uint64_t, entityName *C.char, id *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	store := hooks.Entity
	if store == nil {
		return makeErrBuf(500, "tako: no entity backend wired")
	}
	kind := C.GoString(entityName)
	docID := C.GoString(id)

	doc, err := store.Get(ctx, kind, docID)
	if err != nil {
		if errors.Is(err, backends.ErrNotFound) {
			return makeErrBuf(404, "not found")
		}
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(doc)
}

//export tako_c_entity_put
func tako_c_entity_put(ctxID C.uint64_t, entityName *C.char, id *C.char, jsonData *C.char, jsonLen C.int32_t) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	store := hooks.Entity
	if store == nil {
		return makeErrBuf(500, "tako: no entity backend wired")
	}
	kind := C.GoString(entityName)
	docID := C.GoString(id)
	docBytes := C.GoBytes(unsafe.Pointer(jsonData), jsonLen)
	indexes := getIndexesForKind(kind)

	err := store.Put(ctx, kind, docID, docBytes, indexes)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_entity_delete
func tako_c_entity_delete(ctxID C.uint64_t, entityName *C.char, id *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	store := hooks.Entity
	if store == nil {
		return makeErrBuf(500, "tako: no entity backend wired")
	}
	kind := C.GoString(entityName)
	docID := C.GoString(id)

	err := store.Delete(ctx, kind, docID)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

type queryFilterWire struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

type queryWire struct {
	Filters []queryFilterWire `json:"filters"`
	Order   string            `json:"order"`
	Limit   int               `json:"limit"`
	Cursor  string            `json:"cursor"`
}

//export tako_c_entity_query
func tako_c_entity_query(ctxID C.uint64_t, entityName *C.char, querySpecJson *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	store := hooks.Entity
	if store == nil {
		return makeErrBuf(500, "tako: no entity backend wired")
	}
	kind := C.GoString(entityName)
	rawQuery := C.GoString(querySpecJson)

	var qSpec queryWire
	if err := json.Unmarshal([]byte(rawQuery), &qSpec); err != nil {
		return makeErrBuf(400, fmt.Sprintf("invalid query spec: %v", err))
	}

	var backendFilters []backends.Filter
	for _, f := range qSpec.Filters {
		backendFilters = append(backendFilters, backends.Filter{
			Field: f.Field,
			Op:    f.Op,
			Value: f.Value,
		})
	}
	bQuery := backends.EntityQuery{
		Filters: backendFilters,
		Order:   qSpec.Order,
		Limit:   qSpec.Limit,
	}

	docs, err := store.Query(ctx, kind, bQuery)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}

	out, err := json.Marshal(docs)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(out)
}

//export tako_c_entity_tx
func tako_c_entity_tx(ctxID C.uint64_t, entityName *C.char, cb C.tako_tx_cb, userData unsafe.Pointer) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	store := hooks.Entity
	if store == nil {
		return makeErrBuf(500, "tako: no entity backend wired")
	}
	kind := C.GoString(entityName)
	indexes := getIndexesForKind(kind)

	err := store.Tx(ctx, kind, indexes, func(tx backends.EntityTxOps) error {
		txID := atomic.AddUint64(&txCounter, 1)
		txRegistry.Store(txID, tx)
		defer txRegistry.Delete(txID)

		res := C.invoke_tx_cb(cb, C.uint64_t(txID), userData)
		defer tako_c_free(res)

		if res.status != 0 {
			if res.err != nil {
				return errors.New(C.GoString(res.err))
			}
			return fmt.Errorf("transaction callback returned status %d", res.status)
		}
		return nil
	})

	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_tx_get
func tako_c_tx_get(txID C.uint64_t, entityName *C.char, id *C.char) C.tako_buf_t {
	val, ok := txRegistry.Load(uint64(txID))
	if !ok {
		return makeErrBuf(500, "invalid or expired transaction handle")
	}
	tx := val.(backends.EntityTxOps)
	docID := C.GoString(id)

	doc, err := tx.Get(docID)
	if err != nil {
		if errors.Is(err, backends.ErrNotFound) {
			return makeErrBuf(404, "not found")
		}
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(doc)
}

//export tako_c_tx_put
func tako_c_tx_put(txID C.uint64_t, entityName *C.char, id *C.char, jsonData *C.char, jsonLen C.int32_t) C.tako_buf_t {
	val, ok := txRegistry.Load(uint64(txID))
	if !ok {
		return makeErrBuf(500, "invalid or expired transaction handle")
	}
	tx := val.(backends.EntityTxOps)
	docID := C.GoString(id)
	docBytes := C.GoBytes(unsafe.Pointer(jsonData), jsonLen)

	if err := tx.Put(docID, docBytes); err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_tx_delete
func tako_c_tx_delete(txID C.uint64_t, entityName *C.char, id *C.char) C.tako_buf_t {
	val, ok := txRegistry.Load(uint64(txID))
	if !ok {
		return makeErrBuf(500, "invalid or expired transaction handle")
	}
	tx := val.(backends.EntityTxOps)
	docID := C.GoString(id)

	if err := tx.Delete(docID); err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

// Bucket store operations
//export tako_c_bucket_put
func tako_c_bucket_put(ctxID C.uint64_t, bucketName *C.char, key *C.char, data *C.char, len C.int32_t, contentType *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	bkt := hooks.Bucket
	if bkt == nil {
		return makeErrBuf(500, "tako: no bucket backend wired")
	}
	bName := C.GoString(bucketName)
	k := C.GoString(key)
	bytesData := C.GoBytes(unsafe.Pointer(data), len)
	cType := C.GoString(contentType)

	blob := backends.BlobData{
		Reader:      bytes.NewReader(bytesData),
		ContentType: cType,
		Size:        int64(len),
	}
	err := bkt.Put(ctx, bName, k, blob)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_bucket_get
func tako_c_bucket_get(ctxID C.uint64_t, bucketName *C.char, key *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	bkt := hooks.Bucket
	if bkt == nil {
		return makeErrBuf(500, "tako: no bucket backend wired")
	}
	bName := C.GoString(bucketName)
	k := C.GoString(key)

	blob, err := bkt.Get(ctx, bName, k)
	if err != nil {
		if errors.Is(err, backends.ErrNotFound) {
			return makeErrBuf(404, "not found")
		}
		return makeErrBuf(500, err.Error())
	}
	defer func() {
		if c, ok := blob.Reader.(io.Closer); ok {
			_ = c.Close()
		}
	}()
	data, err := io.ReadAll(blob.Reader)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(data)
}

//export tako_c_bucket_delete
func tako_c_bucket_delete(ctxID C.uint64_t, bucketName *C.char, key *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	bkt := hooks.Bucket
	if bkt == nil {
		return makeErrBuf(500, "tako: no bucket backend wired")
	}
	bName := C.GoString(bucketName)
	k := C.GoString(key)

	err := bkt.Delete(ctx, bName, k)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_bucket_signed_url
func tako_c_bucket_signed_url(ctxID C.uint64_t, bucketName *C.char, key *C.char, method *C.char, contentType *C.char, ttlSec C.int32_t) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	signer := hooks.Signer
	if signer == nil {
		return makeErrBuf(500, "tako: no URL signer wired")
	}
	bName := C.GoString(bucketName)
	k := C.GoString(key)
	m := C.GoString(method)
	cType := C.GoString(contentType)
	ttl := time.Duration(ttlSec) * time.Second

	var url string
	var err error
	if strings.ToUpper(m) == "PUT" {
		url, err = signer.SignPut(ctx, bName, k, ttl, backends.SignConstraints{ContentType: cType})
	} else {
		url, err = signer.SignGet(ctx, bName, k, ttl)
	}
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf([]byte(url))
}

// Queue and Topic brokers
//export tako_c_queue_publish
func tako_c_queue_publish(ctxID C.uint64_t, queueName *C.char, jsonData *C.char, jsonLen C.int32_t, delaySec C.int32_t) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	q := hooks.Queue
	if q == nil {
		return makeErrBuf(500, "tako: no queue backend wired")
	}
	qName := C.GoString(queueName)
	data := C.GoBytes(unsafe.Pointer(jsonData), jsonLen)
	var opts backends.PublishOptions
	if delaySec > 0 {
		opts.Delay = time.Duration(delaySec) * time.Second
	}

	err := q.Publish(ctx, qName, data, opts)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_topic_publish
func tako_c_topic_publish(ctxID C.uint64_t, topicName *C.char, jsonData *C.char, jsonLen C.int32_t) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	t := hooks.Topic
	if t == nil {
		return makeErrBuf(500, "tako: no topic backend wired")
	}
	tName := C.GoString(topicName)
	data := C.GoBytes(unsafe.Pointer(jsonData), jsonLen)

	err := t.Publish(ctx, tName, data)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

// Cache, Lease, Secret, Metrics
//export tako_c_cache_get
func tako_c_cache_get(ctxID C.uint64_t, cacheName *C.char, key *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	c := hooks.Cache
	if c == nil {
		return makeErrBuf(500, "tako: no cache backend wired")
	}
	cName := C.GoString(cacheName)
	k := C.GoString(key)

	val, err := c.Get(ctx, cName, k)
	if err != nil {
		if errors.Is(err, backends.ErrNotFound) {
			return makeErrBuf(404, "not found")
		}
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(val)
}

//export tako_c_cache_set
func tako_c_cache_set(ctxID C.uint64_t, cacheName *C.char, key *C.char, valJson *C.char, valLen C.int32_t, ttlSec C.int32_t) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	c := hooks.Cache
	if c == nil {
		return makeErrBuf(500, "tako: no cache backend wired")
	}
	cName := C.GoString(cacheName)
	k := C.GoString(key)
	data := C.GoBytes(unsafe.Pointer(valJson), valLen)
	ttl := time.Duration(ttlSec) * time.Second

	err := c.Put(ctx, cName, k, data, ttl)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_cache_delete
func tako_c_cache_delete(ctxID C.uint64_t, cacheName *C.char, key *C.char) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	c := hooks.Cache
	if c == nil {
		return makeErrBuf(500, "tako: no cache backend wired")
	}
	cName := C.GoString(cacheName)
	k := C.GoString(key)

	err := c.Delete(ctx, cName, k)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

//export tako_c_lock_with_lease
func tako_c_lock_with_lease(ctxID C.uint64_t, lockName *C.char, ttlSec C.int32_t, cb C.tako_cron_cb, userData unsafe.Pointer) C.tako_buf_t {
	ctx, _ := getCtx(uint64(ctxID))
	lease := hooks.Lease
	if lease == nil {
		return makeErrBuf(500, "tako: no lease backend wired")
	}
	name := C.GoString(lockName)
	ttl := time.Duration(ttlSec) * time.Second
	owner := hooks.LeaseOwner

	held, token, err := lease.TryAcquire(ctx, name, owner, ttl)
	if err != nil {
		return makeErrBuf(500, err.Error())
	}
	if !held {
		return makeErrBuf(409, "lease held")
	}
	defer func() {
		_ = lease.Release(context.Background(), name, owner, token)
	}()

	res := C.invoke_cron_cb(cb, ctxID, userData)
	return res
}

//export tako_c_secret_get
func tako_c_secret_get(ctxID C.uint64_t, secretName *C.char) C.tako_buf_t {
	sName := C.GoString(secretName)
	val, ok := os.LookupEnv(sName)
	if !ok {
		return makeErrBuf(404, fmt.Sprintf("secret %q not resolved", sName))
	}
	return makeOkBuf([]byte(val))
}

//export tako_c_metric_inc
func tako_c_metric_inc(ctxID C.uint64_t, counterName *C.char, delta C.int64_t) {
	ctx, _ := getCtx(uint64(ctxID))
	m := hooks.Metrics
	if m != nil {
		name := C.GoString(counterName)
		m.Add(ctx, name, nil, int64(delta))
	}
}

//export tako_c_metric_observe
func tako_c_metric_observe(ctxID C.uint64_t, histName *C.char, val C.double) {
	ctx, _ := getCtx(uint64(ctxID))
	m := hooks.Metrics
	if m != nil {
		name := C.GoString(histName)
		m.Observe(ctx, name, nil, float64(val))
	}
}

// TestApp harness
//export tako_c_testapp_new
func tako_c_testapp_new() C.uint64_t {
	tmpDir, err := os.MkdirTemp("", "tako-cfire-test-*")
	if err != nil {
		return 0
	}
	app, err := boot.Local(boot.Options{
		Dir:     tmpDir,
		BaseURL: "http://tako.test",
		Queue:   local.QueueOptions{MaxAttempts: 5, Backoff: 5 * time.Millisecond},
	})
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return 0
	}
	app.Dispatcher.UseManualClock()

	appID := atomic.AddUint64(&appCounter, 1)
	inst := &testAppInstance{
		app:        app,
		dir:        tmpDir,
		mux:        http.NewServeMux(),
		handlerMap: make(map[string]http.Handler),
	}
	appRegistry.Store(appID, inst)
	return C.uint64_t(appID)
}

//export tako_c_testapp_close
func tako_c_testapp_close(appID C.uint64_t) {
	val, ok := appRegistry.Load(uint64(appID))
	if !ok {
		return
	}
	appRegistry.Delete(uint64(appID))
	inst := val.(*testAppInstance)
	_ = inst.app.Dispatcher.WaitIdle(10 * time.Second)
	_ = inst.app.Close()
	_ = os.RemoveAll(inst.dir)
}

//export tako_c_testapp_request
func tako_c_testapp_request(appID C.uint64_t, method *C.char, path *C.char, body *C.char, bodyLen C.int32_t, userJson *C.char) C.tako_buf_t {
	val, ok := appRegistry.Load(uint64(appID))
	if !ok {
		return makeErrBuf(500, "invalid app id")
	}
	_ = val
	m := C.GoString(method)
	p := C.GoString(path)
	var bodyBytes []byte
	if body != nil && bodyLen > 0 {
		bodyBytes = C.GoBytes(unsafe.Pointer(body), bodyLen)
	}

	req, err := http.NewRequest(m, p, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return makeErrBuf(400, err.Error())
	}
	if userJson != nil {
		req.Header.Set("X-Tako-Test-User", C.GoString(userJson))
	}

	rec := &responseRecorder{
		header: make(http.Header),
		body:   new(strings.Builder),
		code:   http.StatusOK,
	}
	globalMux.ServeHTTP(rec, req)

	respBytes := []byte(rec.body.String())
	cBuf := makeOkBuf(respBytes)
	cBuf.status = C.int32_t(rec.code)
	return cBuf
}

type responseRecorder struct {
	header http.Header
	body   *strings.Builder
	code   int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *responseRecorder) WriteHeader(code int)        { r.code = code }

//export tako_c_testapp_drain
func tako_c_testapp_drain(appID C.uint64_t) C.tako_buf_t {
	val, ok := appRegistry.Load(uint64(appID))
	if !ok {
		return makeErrBuf(500, "invalid app id")
	}
	inst := val.(*testAppInstance)
	if err := inst.app.Dispatcher.WaitIdle(30 * time.Second); err != nil {
		return makeErrBuf(500, err.Error())
	}
	return makeOkBuf(nil)
}

func makeCString(s string) *C.char {
	return C.CString(s)
}

func freeCString(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

func makeCBytes(b []byte) *C.char {
	return (*C.char)(C.CBytes(b))
}

func goString(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}

func goBytes(p *C.char, len C.int32_t) []byte {
	if p == nil || len <= 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(p), len)
}

func makeCInt32(n int) C.int32_t {
	return C.int32_t(n)
}

func main() {}
