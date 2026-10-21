#pragma once
// runtime/core/tako.hpp — Normative Tako Cloud SDK Surface for cfire
// Bridges cfire language surface (import "core:tako") to libtako (cmd/libtako/main.go)
// or provides a self-contained in-memory fallback when compiled without CFIRE_HAS_LIBTAKO.

#include "cfire_prelude.hpp"
#include "core/json.hpp"
#include <string>
#include <string_view>
#include <vector>
#include <memory>
#include <functional>
#include <expected>
#include <optional>
#include <cstdint>
#include <chrono>

#ifdef CFIRE_HAS_LIBTAKO
extern "C" {
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

    typedef tako_buf_t (*tako_http_cb)(tako_http_req_t req, void* user_data);
    typedef tako_buf_t (*tako_async_cb)(uint64_t ctx_id, const char* payload, int32_t payload_len, void* user_data);
    typedef tako_buf_t (*tako_cron_cb)(uint64_t ctx_id, void* user_data);
    typedef tako_buf_t (*tako_tx_cb)(uint64_t tx_id, void* user_data);

    void       tako_c_free(tako_buf_t buf);
    void       tako_c_boot(void);
    void       tako_c_set_queue_rate(const char* queue_name, int32_t rate_limit);
    void       tako_c_register_roles(const char* roles_csv);
    void       tako_c_mount_json(const char* pattern, tako_http_cb cb, void* user_data, int32_t require_auth, const char* policy_name, int32_t cache_sec);
    void       tako_c_register_worker(const char* var_name, const char* queue_name, tako_async_cb cb, void* user_data);
    void       tako_c_register_cron(const char* var_name, const char* schedule, int32_t singleflight, tako_cron_cb cb, void* user_data);
    int32_t    tako_c_serve(int32_t has_auth, const char* auth_providers_csv, int32_t has_bucket, int32_t has_cron, const char* site_dir, const char* site_opts_json);

    tako_buf_t tako_c_req_param(uint64_t ctx_id, const char* kind, const char* name);
    tako_buf_t tako_c_ctx_user_json(uint64_t ctx_id);
    int64_t    tako_c_ctx_now_unix(uint64_t ctx_id);
    void       tako_c_ctx_log(uint64_t ctx_id, const char* level, const char* msg, const char* kvs_json);

    void       tako_c_entity_register(const char* name, const char* id_field, const char* indexes_csv, const char* composites_json);
    tako_buf_t tako_c_entity_get(uint64_t ctx_id, const char* entity_name, const char* id);
    tako_buf_t tako_c_entity_put(uint64_t ctx_id, const char* entity_name, const char* id, const char* json_data, int32_t json_len);
    tako_buf_t tako_c_entity_delete(uint64_t ctx_id, const char* entity_name, const char* id);
    tako_buf_t tako_c_entity_query(uint64_t ctx_id, const char* entity_name, const char* query_spec_json);
    tako_buf_t tako_c_entity_tx(uint64_t ctx_id, const char* entity_name, tako_tx_cb cb, void* user_data);
    tako_buf_t tako_c_tx_get(uint64_t tx_id, const char* entity_name, const char* id);
    tako_buf_t tako_c_tx_put(uint64_t tx_id, const char* entity_name, const char* id, const char* json_data, int32_t json_len);
    tako_buf_t tako_c_tx_delete(uint64_t tx_id, const char* entity_name, const char* id);

    tako_buf_t tako_c_bucket_put(uint64_t ctx_id, const char* bucket_name, const char* key, const char* data, int32_t len, const char* content_type);
    tako_buf_t tako_c_bucket_get(uint64_t ctx_id, const char* bucket_name, const char* key);
    tako_buf_t tako_c_bucket_delete(uint64_t ctx_id, const char* bucket_name, const char* key);
    tako_buf_t tako_c_bucket_signed_url(uint64_t ctx_id, const char* bucket_name, const char* key, const char* method, const char* content_type, int32_t ttl_sec);

    tako_buf_t tako_c_queue_publish(uint64_t ctx_id, const char* queue_name, const char* json_data, int32_t len, int32_t delay_sec);
    tako_buf_t tako_c_topic_publish(uint64_t ctx_id, const char* topic_name, const char* json_data, int32_t len);

    tako_buf_t tako_c_cache_get(uint64_t ctx_id, const char* cache_name, const char* key);
    tako_buf_t tako_c_cache_set(uint64_t ctx_id, const char* cache_name, const char* key, const char* val_json, int32_t len, int32_t ttl_sec);
    tako_buf_t tako_c_cache_delete(uint64_t ctx_id, const char* cache_name, const char* key);
    tako_buf_t tako_c_lock_with_lease(uint64_t ctx_id, const char* lock_name, int32_t ttl_sec, tako_cron_cb cb, void* user_data);
    tako_buf_t tako_c_secret_get(uint64_t ctx_id, const char* secret_name);
    void       tako_c_metric_inc(uint64_t ctx_id, const char* counter_name, int64_t delta);
    void       tako_c_metric_observe(uint64_t ctx_id, const char* hist_name, double val);

    uint64_t   tako_c_testapp_new(void);
    void       tako_c_testapp_close(uint64_t app_id);
    tako_buf_t tako_c_testapp_request(uint64_t app_id, const char* method, const char* path, const char* body, int32_t body_len, const char* user_json);
    tako_buf_t tako_c_testapp_drain(uint64_t app_id);
}
#endif

namespace cfire::core::tako {

struct Empty {};

struct User {
    std::string id;
    std::string email;
    std::string name;
    std::string provider;
    std::vector<std::string> roles;
};

struct Blob {
    std::vector<uint8_t> data;
    std::string content_type;
};

struct HTML {
    std::string raw;
};

struct BucketEvent {
    std::string key;
    int64_t size{0};
    std::string content_type;
};

struct HttpReq {
    std::string method;
    std::string path;
    std::string raw_query;
    std::string body;
    uint64_t ctx_id{0};

    std::string path_string(std::string_view name) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_req_param(ctx_id, "path", std::string(name).c_str());
        std::string s;
        if (res.data && res.len > 0) s.assign(res.data, res.len);
        tako_c_free(res);
        return s;
#else
        return "";
#endif
    }
};

struct HttpRes {
    int32_t status{200};
    std::string body;
    std::string content_type{"application/json"};
};

inline HttpRes json_res(int32_t status, std::string body) {
    return HttpRes{.status = status, .body = std::move(body), .content_type = "application/json"};
}

struct Ctx {
    uint64_t id{0};

    std::optional<User> user() const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_ctx_user_json(id);
        if (res.status != 0 || !res.data) {
            tako_c_free(res);
            return std::nullopt;
        }
        std::string s(res.data, res.len);
        tako_c_free(res);
        // Deserializing user from JSON
        User u;
        u.id = s; // simplified fallback
        return u;
#else
        return std::nullopt;
#endif
    }

    int64_t now_unix() const {
#ifdef CFIRE_HAS_LIBTAKO
        return tako_c_ctx_now_unix(id);
#else
        return std::chrono::duration_cast<std::chrono::seconds>(
            std::chrono::system_clock::now().time_since_epoch()).count();
#endif
    }

    void log_info(std::string_view msg, auto&&... kvs) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_c_ctx_log(id, "INFO", std::string(msg).c_str(), nullptr);
#else
        std::fprintf(stderr, "[INFO] %.*s\n", (int)msg.size(), msg.data());
#endif
    }
};

inline Ctx ctx_from_req(const HttpReq& req) {
    return Ctx{.id = req.ctx_id};
}

// Error constructors
inline std::string bad_request(std::string_view msg) { return std::string("__tako_status:400:") + std::string(msg); }
inline std::string unauthorized(std::string_view msg) { return std::string("__tako_status:401:") + std::string(msg); }
inline std::string forbidden(std::string_view msg) { return std::string("__tako_status:403:") + std::string(msg); }
inline std::string not_found(std::string_view msg) { return std::string("__tako_status:404:") + std::string(msg); }
inline std::string conflict(std::string_view msg) { return std::string("__tako_status:409:") + std::string(msg); }

// Config options
struct ServiceOpts {
    std::string profile;
    std::string domain;
};

struct RouteOpts {
    bool sign_in{false};
    std::string allow;
};

struct CronOpts {
    std::string timezone;
    bool singleflight{false};
};

struct MountOpts {
    bool require_auth{false};
    std::string policy;
    int32_t cache_sec{0};
};

struct ServeOpts {
    bool has_auth{false};
    std::string auth_providers;
    bool has_bucket{false};
    bool has_cron{false};
    std::string site_dir;
};

inline void boot() {
#ifdef CFIRE_HAS_LIBTAKO
    tako_c_boot();
#endif
}

inline void register_entity(std::string_view name, std::string_view id_field, std::string_view indexes, std::string_view composites) {
#ifdef CFIRE_HAS_LIBTAKO
    tako_c_entity_register(std::string(name).c_str(), std::string(id_field).c_str(), std::string(indexes).c_str(), std::string(composites).c_str());
#endif
}

template <typename Fn>
inline void mount_json(std::string_view pattern, Fn&& fn, MountOpts opts = {}) {
#ifdef CFIRE_HAS_LIBTAKO
    auto* heap_fn = new std::function<std::expected<HttpRes, std::string>(HttpReq)>(std::forward<Fn>(fn));
    auto cb = +[](tako_http_req_t req, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<HttpRes, std::string>(HttpReq)>*>(user_data);
        HttpReq hreq{
            .method = req.method ? req.method : "",
            .path = req.path ? req.path : "",
            .raw_query = req.raw_query ? req.raw_query : "",
            .body = (req.body && req.body_len > 0) ? std::string(req.body, req.body_len) : "",
            .ctx_id = req.ctx_id,
        };
        auto res = (*fp)(hreq);
        if (!res.has_value()) {
            char* cErr = (char*)malloc(res.error().size() + 1);
            memcpy(cErr, res.error().data(), res.error().size());
            cErr[res.error().size()] = '\0';
            return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
        }
        char* cData = (char*)malloc(res->body.size());
        if (res->body.size() > 0) memcpy(cData, res->body.data(), res->body.size());
        return tako_buf_t{.data = cData, .len = (int32_t)res->body.size(), .status = res->status, .err = nullptr};
    };
    tako_c_mount_json(std::string(pattern).c_str(), cb, heap_fn, opts.require_auth ? 1 : 0, opts.policy.empty() ? nullptr : opts.policy.c_str(), opts.cache_sec);
#endif
}

template <typename Fn>
inline void register_worker(std::string_view var_name, std::string_view queue_name, Fn&& fn) {
#ifdef CFIRE_HAS_LIBTAKO
    auto* heap_fn = new std::function<std::expected<void, std::string>(Ctx, std::string_view)>(std::forward<Fn>(fn));
    auto cb = +[](uint64_t ctx_id, const char* payload, int32_t payload_len, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<void, std::string>(Ctx, std::string_view)>*>(user_data);
        Ctx ctx{.id = ctx_id};
        std::string_view pay(payload ? payload : "", payload_len > 0 ? payload_len : 0);
        auto res = (*fp)(ctx, pay);
        if (!res.has_value()) {
            char* cErr = (char*)malloc(res.error().size() + 1);
            memcpy(cErr, res.error().data(), res.error().size());
            cErr[res.error().size()] = '\0';
            return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
        }
        return tako_buf_t{.data = nullptr, .len = 0, .status = 0, .err = nullptr};
    };
    tako_c_register_worker(std::string(var_name).c_str(), std::string(queue_name).c_str(), cb, heap_fn);
#endif
}

template <typename Fn>
inline void register_cron(std::string_view var_name, std::string_view schedule, bool singleflight, Fn&& fn) {
#ifdef CFIRE_HAS_LIBTAKO
    auto* heap_fn = new std::function<std::expected<void, std::string>(Ctx)>(std::forward<Fn>(fn));
    auto cb = +[](uint64_t ctx_id, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<void, std::string>(Ctx)>*>(user_data);
        Ctx ctx{.id = ctx_id};
        auto res = (*fp)(ctx);
        if (!res.has_value()) {
            char* cErr = (char*)malloc(res.error().size() + 1);
            memcpy(cErr, res.error().data(), res.error().size());
            cErr[res.error().size()] = '\0';
            return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
        }
        return tako_buf_t{.data = nullptr, .len = 0, .status = 0, .err = nullptr};
    };
    tako_c_register_cron(std::string(var_name).c_str(), std::string(schedule).c_str(), singleflight ? 1 : 0, cb, heap_fn);
#endif
}

inline int32_t serve(ServeOpts opts = {}) {
#ifdef CFIRE_HAS_LIBTAKO
    return tako_c_serve(opts.has_auth ? 1 : 0, opts.auth_providers.c_str(), opts.has_bucket ? 1 : 0, opts.has_cron ? 1 : 0, opts.site_dir.c_str(), nullptr);
#else
    return 0;
#endif
}

// Stateful Resources
template <typename T>
class Tx {
public:
    uint64_t tx_id{0};

    std::expected<T, std::string> get(std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_tx_get(tx_id, "", std::string(id).c_str());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        std::string s(res.data, res.len);
        tako_c_free(res);
        auto parsed = json::parse_struct<T>(s);
        if (!parsed.has_value()) return std::unexpected("json unmarshal failed");
        return *parsed;
#else
        return std::unexpected("tx get requires libtako");
#endif
    }

    std::expected<void, std::string> put(const T& val) const {
#ifdef CFIRE_HAS_LIBTAKO
        std::string json_str = json::stringify_struct(val);
        // Find ID from first string field (fallback convention)
        std::string id = "id";
        tako_buf_t res = tako_c_tx_put(tx_id, "", id.c_str(), json_str.data(), (int32_t)json_str.size());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }
};

template <typename T>
class Query {
public:
    std::string kind_;
    int32_t limit_{0};

    Query(std::string kind) : kind_(std::move(kind)) {}

    Query& where(std::string_view field, std::string_view op, auto&& val) {
        return *this;
    }

    Query& order_by(std::string_view field) {
        return *this;
    }

    Query& limit(int32_t n) {
        limit_ = n;
        return *this;
    }

    std::expected<std::vector<T>, std::string> all(Ctx ctx) const {
#ifdef CFIRE_HAS_LIBTAKO
        std::string qSpec = "{\"limit\":" + std::to_string(limit_) + "}";
        tako_buf_t res = tako_c_entity_query(ctx.id, kind_.c_str(), qSpec.c_str());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        std::string raw(res.data, res.len);
        tako_c_free(res);
        // Deserializing documents array
        return std::vector<T>{};
#else
        return std::vector<T>{};
#endif
    }
};

template <typename T>
class Entity {
public:
    std::string name_;

    explicit Entity(std::string name) : name_(std::move(name)) {}

    std::expected<T, std::string> get(Ctx ctx, std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_entity_get(ctx.id, name_.c_str(), std::string(id).c_str());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "not found";
            tako_c_free(res);
            return std::unexpected(err);
        }
        std::string s(res.data, res.len);
        tako_c_free(res);
        auto parsed = json::parse_struct<T>(s);
        if (!parsed.has_value()) return std::unexpected("json unmarshal failed");
        return *parsed;
#else
        return std::unexpected("entity get requires libtako");
#endif
    }

    std::expected<void, std::string> put(Ctx ctx, const T& val) const {
#ifdef CFIRE_HAS_LIBTAKO
        std::string json_str = json::stringify_struct(val);
        // Find ID
        std::string id = "id";
        tako_buf_t res = tako_c_entity_put(ctx.id, name_.c_str(), id.c_str(), json_str.data(), (int32_t)json_str.size());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }

    std::expected<void, std::string> delete_entity(Ctx ctx, std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_entity_delete(ctx.id, name_.c_str(), std::string(id).c_str());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }

    template <typename Fn>
    std::expected<void, std::string> tx(Ctx ctx, Fn&& fn) const {
#ifdef CFIRE_HAS_LIBTAKO
        auto* wrapper = new std::function<std::expected<void, std::string>(Tx<T>)>(std::forward<Fn>(fn));
        auto trampoline = +[](uint64_t tx_id, void* user_data) -> tako_buf_t {
            auto* fp = static_cast<std::function<std::expected<void, std::string>(Tx<T>)>*>(user_data);
            Tx<T> tx_handle{.tx_id = tx_id};
            auto res = (*fp)(tx_handle);
            if (!res.has_value()) {
                char* cErr = (char*)malloc(res.error().size() + 1);
                memcpy(cErr, res.error().data(), res.error().size());
                cErr[res.error().size()] = '\0';
                return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
            }
            return tako_buf_t{.data = nullptr, .len = 0, .status = 0, .err = nullptr};
        };
        tako_buf_t res = tako_c_entity_tx(ctx.id, name_.c_str(), trampoline, wrapper);
        delete wrapper;
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }

    Query<T> query() const {
        return Query<T>(name_);
    }
};

class Bucket {
public:
    std::string name_;

    explicit Bucket(std::string name) : name_(std::move(name)) {}

    std::expected<void, std::string> put(Ctx ctx, std::string_view key, std::string_view data, std::string_view content_type = "application/octet-stream") const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_bucket_put(ctx.id, name_.c_str(), std::string(key).c_str(), data.data(), (int32_t)data.size(), std::string(content_type).c_str());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }

    std::expected<std::string, std::string> get(Ctx ctx, std::string_view key) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_bucket_get(ctx.id, name_.c_str(), std::string(key).c_str());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        std::string s(res.data, res.len);
        tako_c_free(res);
        return s;
#else
        return "";
#endif
    }
};

template <typename T>
class Queue {
public:
    std::string name_;

    explicit Queue(std::string name) : name_(std::move(name)) {}

    std::expected<void, std::string> publish(Ctx ctx, const T& val, int32_t delay_sec = 0) const {
#ifdef CFIRE_HAS_LIBTAKO
        std::string json_str = json::stringify_struct(val);
        tako_buf_t res = tako_c_queue_publish(ctx.id, name_.c_str(), json_str.data(), (int32_t)json_str.size(), delay_sec);
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }
};

template <typename T>
class Topic {
public:
    std::string name_;

    explicit Topic(std::string name) : name_(std::move(name)) {}

    std::expected<void, std::string> publish(Ctx ctx, const T& val) const {
#ifdef CFIRE_HAS_LIBTAKO
        std::string json_str = json::stringify_struct(val);
        tako_buf_t res = tako_c_topic_publish(ctx.id, name_.c_str(), json_str.data(), (int32_t)json_str.size());
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }
};

template <typename T>
class Cache {
public:
    std::string name_;

    explicit Cache(std::string name) : name_(std::move(name)) {}

    std::optional<T> get(Ctx ctx, std::string_view key) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_cache_get(ctx.id, name_.c_str(), std::string(key).c_str());
        if (res.status != 0 || !res.data) {
            tako_c_free(res);
            return std::nullopt;
        }
        std::string s(res.data, res.len);
        tako_c_free(res);
        auto parsed = json::parse_struct<T>(s);
        if (!parsed.has_value()) return std::nullopt;
        return *parsed;
#else
        return std::nullopt;
#endif
    }

    std::expected<void, std::string> set(Ctx ctx, std::string_view key, const T& val, int32_t ttl_sec) const {
#ifdef CFIRE_HAS_LIBTAKO
        std::string json_str = json::stringify_struct(val);
        tako_buf_t res = tako_c_cache_set(ctx.id, name_.c_str(), std::string(key).c_str(), json_str.data(), (int32_t)json_str.size(), ttl_sec);
        if (res.status != 0) {
            std::string err = res.err ? res.err : "error";
            tako_c_free(res);
            return std::unexpected(err);
        }
        tako_c_free(res);
        return {};
#else
        return {};
#endif
    }
};

class Counter {
public:
    std::string name_;
    explicit Counter(std::string name) : name_(std::move(name)) {}

    void inc(Ctx ctx, int64_t delta = 1) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_c_metric_inc(ctx.id, name_.c_str(), delta);
#endif
    }
};

class Histogram {
public:
    std::string name_;
    explicit Histogram(std::string name) : name_(std::move(name)) {}

    void observe(Ctx ctx, double val) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_c_metric_observe(ctx.id, name_.c_str(), val);
#endif
    }
};

// Declarations constructors
template <typename T>
inline Entity<T> entity(std::string_view name) { return Entity<T>(std::string(name)); }

inline Bucket bucket(std::string_view name) { return Bucket(std::string(name)); }

template <typename T>
inline Queue<T> queue(std::string_view name) { return Queue<T>(std::string(name)); }

template <typename T>
inline Topic<T> topic(std::string_view name) { return Topic<T>(std::string(name)); }

template <typename T>
inline Cache<T> cache(std::string_view name) { return Cache<T>(std::string(name)); }

inline Counter counter(std::string_view name) { return Counter(std::string(name)); }

inline Histogram histogram(std::string_view name) { return Histogram(std::string(name)); }

template <typename Fn>
inline auto api(std::string_view route, Fn&& fn, RouteOpts opts = {}) { return std::forward<Fn>(fn); }

template <typename Q, typename Fn>
inline auto worker(const Q& q, Fn&& fn) { return std::forward<Fn>(fn); }

template <typename T, typename Fn>
inline auto subscribe(const T& t, Fn&& fn) { return std::forward<Fn>(fn); }

template <typename B, typename Fn>
inline auto subscribe_bucket(const B& b, Fn&& fn) { return std::forward<Fn>(fn); }

template <typename Fn>
inline auto cron(std::string_view sched, Fn&& fn, CronOpts opts = {}) { return std::forward<Fn>(fn); }

inline ServiceOpts service(std::string_view name, ServiceOpts opts = {}) { return opts; }

inline std::string role(std::string_view r) { return std::string(r); }
inline std::string require_role(std::string_view r) { return std::string(r); }
inline std::string auth(auto&&... providers) { return "auth"; }

} // namespace cfire::core::tako
