#pragma once
// sdk/cfire/include/tako.hpp — Normative Tako Cloud SDK Surface for cfire
// Bridges cfire language surface (import "core:tako") to libtako (cmd/libtako/main.go)
// and provides a complete in-memory local runtime when compiled without CFIRE_HAS_LIBTAKO.

#include "cfire_prelude.hpp"
#include "core/json.hpp"
#include <string>
#include <string_view>
#include <vector>
#include <memory>
#include <functional>
#include <expected>
#include <optional>
#include <unordered_map>
#include <cstdint>
#include <chrono>
#include <cstdlib>
#include <cstring>

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

namespace cfire::tako {

namespace detail {

struct LocalStore {
    std::unordered_map<std::string, std::unordered_map<std::string, std::string>> entities;
    std::unordered_map<std::string, std::unordered_map<std::string, std::pair<std::string, std::string>>> buckets;
    std::unordered_map<std::string, std::unordered_map<std::string, std::string>> caches;
    std::unordered_map<std::string, std::vector<std::string>> queues;
    std::unordered_map<std::string, std::vector<std::function<std::expected<void, cfire::Error>(uint64_t, std::string_view)>>> workers;
    std::unordered_map<std::string, std::string> secrets;
    std::unordered_map<std::string, int64_t> counters;
    std::unordered_map<std::string, std::vector<double>> histograms;

    void reset() {
        entities.clear();
        buckets.clear();
        caches.clear();
        queues.clear();
        secrets.clear();
        counters.clear();
        histograms.clear();
    }
};

inline LocalStore& local_store() {
    static LocalStore s;
    return s;
}

template <typename T>
inline std::string extract_entity_id(const std::string& json_str) {
    auto dyn = cfire::json::parse_dynamic(json_str);
    if (!dyn || !dyn->is_object()) return "id";
    // Check cfire reflection tags for `entity:"id"`
    if constexpr (requires { cfire::reflect_struct<T>::field_count; }) {
        for (std::size_t i = 0; i < cfire::reflect_struct<T>::field_count; ++i) {
            std::string_view tag = cfire::reflect_struct<T>::field_tags[i];
            if (tag.find("entity:\"id\"") != std::string_view::npos) {
                std::string_view fname = cfire::reflect_struct<T>::field_names[i];
                // Also check if json:"..." overrides key name
                std::string jkey(fname);
                if (auto jpos = tag.find("json:\""); jpos != std::string_view::npos) {
                    auto rest = tag.substr(jpos + 6);
                    if (auto endq = rest.find('"'); endq != std::string_view::npos) {
                        jkey = std::string(rest.substr(0, endq));
                    }
                }
                const auto& fval = (*dyn)[jkey];
                if (fval.is_string() && !fval.as_str().empty()) {
                    return std::string(fval.as_str());
                }
            }
        }
    }
    for (const char* candidate : {"id", "code", "slug", "key", "from_slug"}) {
        const auto& v = (*dyn)[candidate];
        if (v.is_string() && !v.as_str().empty()) {
            return std::string(v.as_str());
        }
    }
    return "id";
}

inline std::string parse_query_param(std::string_view raw_query, std::string_view key) {
    std::size_t pos = 0;
    while (pos < raw_query.size()) {
        auto amp = raw_query.find('&', pos);
        auto pair = (amp == std::string_view::npos) ? raw_query.substr(pos) : raw_query.substr(pos, amp - pos);
        auto eq = pair.find('=');
        if (eq != std::string_view::npos && pair.substr(0, eq) == key) {
            return std::string(pair.substr(eq + 1));
        }
        if (amp == std::string_view::npos) break;
        pos = amp + 1;
    }
    return "";
}

} // namespace detail

struct Empty {};

struct User {
    std::string id;
    std::string email;
    std::string name;
    std::string provider;
    std::vector<std::string> roles;
};

struct Blob {
    std::vector<std::byte> data;
    std::string content_type{"application/octet-stream"};
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
    std::unordered_map<std::string, std::string> path_params{};
    std::unordered_map<std::string, std::string> headers{};

    std::string path_string(std::string_view name) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_req_param(ctx_id, "path", std::string(name).c_str());
        std::string s;
        if (res.data && res.len > 0) s.assign(res.data, res.len);
        tako_c_free(res);
        if (!s.empty()) return s;
#endif
        if (auto it = path_params.find(std::string(name)); it != path_params.end()) {
            return it->second;
        }
        return "";
    }

    std::string query_string(std::string_view name) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_req_param(ctx_id, "query", std::string(name).c_str());
        std::string s;
        if (res.data && res.len > 0) s.assign(res.data, res.len);
        tako_c_free(res);
        if (!s.empty()) return s;
#endif
        return detail::parse_query_param(raw_query, name);
    }

    bool query_bool(std::string_view name) const {
        auto s = query_string(name);
        return s == "true" || s == "1" || s == "yes";
    }

    int64_t query_i64(std::string_view name) const {
        auto s = query_string(name);
        if (s.empty()) return 0;
        return std::strtoll(s.c_str(), nullptr, 10);
    }

    double query_f64(std::string_view name) const {
        auto s = query_string(name);
        if (s.empty()) return 0.0;
        return std::strtod(s.c_str(), nullptr);
    }

    std::string form_string(std::string_view name) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_req_param(ctx_id, "form", std::string(name).c_str());
        std::string s;
        if (res.data && res.len > 0) s.assign(res.data, res.len);
        tako_c_free(res);
        if (!s.empty()) return s;
#endif
        return detail::parse_query_param(body, name);
    }

    std::string header_string(std::string_view name) const {
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_req_param(ctx_id, "header", std::string(name).c_str());
        std::string s;
        if (res.data && res.len > 0) s.assign(res.data, res.len);
        tako_c_free(res);
        if (!s.empty()) return s;
#endif
        if (auto it = headers.find(std::string(name)); it != headers.end()) {
            return it->second;
        }
        return "";
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
    std::optional<User> current_user{};

    std::optional<User> user() const {
        if (current_user.has_value()) return current_user;
#ifdef CFIRE_HAS_LIBTAKO
        tako_buf_t res = tako_c_ctx_user_json(id);
        if (res.status != 0 || !res.data) {
            tako_c_free(res);
            return std::nullopt;
        }
        std::string s(res.data, res.len);
        tako_c_free(res);
        User u;
        u.id = s;
        return u;
#else
        return std::nullopt;
#endif
    }

    int64_t now_unix() const {
#ifdef CFIRE_HAS_LIBTAKO
        if (id != 0) return tako_c_ctx_now_unix(id);
#endif
        return std::chrono::duration_cast<std::chrono::seconds>(
            std::chrono::system_clock::now().time_since_epoch()).count();
    }

    void log_info(std::string_view msg, auto&&... /*kvs*/) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (id != 0) {
            tako_c_ctx_log(id, "INFO", std::string(msg).c_str(), nullptr);
            return;
        }
#endif
        (void)msg;
    }
};

inline Ctx ctx_from_req(const HttpReq& req) {
    return Ctx{.id = req.ctx_id};
}

// Error constructors
inline cfire::Error bad_request(std::string_view msg) { return cfire::Error(std::string("__tako_status:400:") + std::string(msg)); }
inline cfire::Error unauthorized(std::string_view msg) { return cfire::Error(std::string("__tako_status:401:") + std::string(msg)); }
inline cfire::Error forbidden(std::string_view msg) { return cfire::Error(std::string("__tako_status:403:") + std::string(msg)); }
inline cfire::Error not_found(std::string_view msg) { return cfire::Error(std::string("__tako_status:404:") + std::string(msg)); }
inline cfire::Error conflict(std::string_view msg) { return cfire::Error(std::string("__tako_status:409:") + std::string(msg)); }

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

struct QueueOpts {
    int32_t rate_limit{0};
    int32_t max_attempts{5};
};

struct SiteOpts {
    std::string build;
    std::string build_out;
    bool spa{false};
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
#else
    (void)name; (void)id_field; (void)indexes; (void)composites;
#endif
}

template <typename Fn>
inline void mount_json(std::string_view pattern, Fn&& fn, MountOpts opts = {}) {
#ifdef CFIRE_HAS_LIBTAKO
    auto* heap_fn = new std::function<std::expected<HttpRes, cfire::Error>(HttpReq)>(std::forward<Fn>(fn));
    auto cb = +[](tako_http_req_t req, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<HttpRes, cfire::Error>>(HttpReq)*>(user_data);
        HttpReq hreq{
            .method = req.method ? req.method : "",
            .path = req.path ? req.path : "",
            .raw_query = req.raw_query ? req.raw_query : "",
            .body = (req.body && req.body_len > 0) ? std::string(req.body, req.body_len) : "",
            .ctx_id = req.ctx_id,
        };
        auto res = (*fp)(hreq);
        if (!res.has_value()) {
            auto msg = res.error().message();
            char* cErr = (char*)std::malloc(msg.size() + 1);
            std::memcpy(cErr, msg.data(), msg.size());
            cErr[msg.size()] = '\0';
            return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
        }
        char* cData = (char*)std::malloc(res->body.size());
        if (res->body.size() > 0) std::memcpy(cData, res->body.data(), res->body.size());
        return tako_buf_t{.data = cData, .len = (int32_t)res->body.size(), .status = res->status, .err = nullptr};
    };
    tako_c_mount_json(std::string(pattern).c_str(), cb, heap_fn, opts.require_auth ? 1 : 0, opts.policy.empty() ? nullptr : opts.policy.c_str(), opts.cache_sec);
#else
    (void)pattern; (void)fn; (void)opts;
#endif
}

template <typename Fn>
inline void register_worker(std::string_view var_name, std::string_view queue_name, Fn&& fn) {
    detail::local_store().workers[std::string(queue_name)].push_back(
        [f = std::forward<Fn>(fn)](uint64_t cid, std::string_view pay) -> std::expected<void, cfire::Error> {
            return f(Ctx{.id = cid}, std::string(pay));
        }
    );
#ifdef CFIRE_HAS_LIBTAKO
    auto* heap_fn = new std::function<std::expected<void, cfire::Error>(Ctx, std::string)>(fn);
    auto cb = +[](uint64_t ctx_id, const char* payload, int32_t payload_len, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<void, cfire::Error>(Ctx, std::string)>*>(user_data);
        Ctx ctx{.id = ctx_id};
        std::string pay(payload ? payload : "", payload_len > 0 ? payload_len : 0);
        auto res = (*fp)(ctx, pay);
        if (!res.has_value()) {
            auto msg = res.error().message();
            char* cErr = (char*)std::malloc(msg.size() + 1);
            std::memcpy(cErr, msg.data(), msg.size());
            cErr[msg.size()] = '\0';
            return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
        }
        return tako_buf_t{.data = nullptr, .len = 0, .status = 0, .err = nullptr};
    };
    tako_c_register_worker(std::string(var_name).c_str(), std::string(queue_name).c_str(), cb, heap_fn);
#else
    (void)var_name;
#endif
}

template <typename Fn>
inline void register_cron(std::string_view var_name, std::string_view schedule, bool singleflight, Fn&& fn) {
#ifdef CFIRE_HAS_LIBTAKO
    auto* heap_fn = new std::function<std::expected<void, cfire::Error>(Ctx)>(std::forward<Fn>(fn));
    auto cb = +[](uint64_t ctx_id, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<void, cfire::Error>(Ctx)>*>(user_data);
        Ctx ctx{.id = ctx_id};
        auto res = (*fp)(ctx);
        if (!res.has_value()) {
            auto msg = res.error().message();
            char* cErr = (char*)std::malloc(msg.size() + 1);
            std::memcpy(cErr, msg.data(), msg.size());
            cErr[msg.size()] = '\0';
            return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
        }
        return tako_buf_t{.data = nullptr, .len = 0, .status = 0, .err = nullptr};
    };
    tako_c_register_cron(std::string(var_name).c_str(), std::string(schedule).c_str(), singleflight ? 1 : 0, cb, heap_fn);
#else
    (void)var_name; (void)schedule; (void)singleflight; (void)fn;
#endif
}

inline int32_t serve(ServeOpts opts = {}) {
#ifdef CFIRE_HAS_LIBTAKO
    return tako_c_serve(opts.has_auth ? 1 : 0, opts.auth_providers.c_str(), opts.has_bucket ? 1 : 0, opts.has_cron ? 1 : 0, opts.site_dir.c_str(), nullptr);
#else
    (void)opts;
    return 0;
#endif
}

// Stateful Resources
template <typename T>
class Tx {
public:
    uint64_t tx_id{0};
    std::string entity_name_;

    std::expected<T, cfire::Error> get(std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (tx_id != 0) {
            tako_buf_t res = tako_c_tx_get(tx_id, entity_name_.c_str(), std::string(id).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            std::string s(res.data, res.len);
            tako_c_free(res);
            return cfire::json::parse<T>(s);
        }
#endif
        auto& tbl = detail::local_store().entities[entity_name_];
        auto it = tbl.find(std::string(id));
        if (it == tbl.end()) return cfire::make_error(not_found("not found"));
        return cfire::json::parse<T>(it->second);
    }

    std::expected<void, cfire::Error> put(const T& val) const {
        std::string json_str = cfire::json::stringify(val);
        std::string id = detail::extract_entity_id<T>(json_str);
        return put(id, val);
    }

    std::expected<void, cfire::Error> put(std::string_view id, const T& val) const {
        std::string json_str = cfire::json::stringify(val);
#ifdef CFIRE_HAS_LIBTAKO
        if (tx_id != 0) {
            tako_buf_t res = tako_c_tx_put(tx_id, entity_name_.c_str(), std::string(id).c_str(), json_str.data(), (int32_t)json_str.size());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#endif
        detail::local_store().entities[entity_name_][std::string(id)] = std::move(json_str);
        return {};
    }

    std::expected<void, cfire::Error> delete_entity(std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (tx_id != 0) {
            tako_buf_t res = tako_c_tx_delete(tx_id, entity_name_.c_str(), std::string(id).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#endif
        detail::local_store().entities[entity_name_].erase(std::string(id));
        return {};
    }

    std::expected<void, cfire::Error> remove(std::string_view id) const { return delete_entity(id); }
    std::expected<void, cfire::Error> del(std::string_view id) const { return delete_entity(id); }
};

template <typename T>
class Query {
public:
    std::string kind_;
    int32_t limit_{0};

    explicit Query(std::string kind) : kind_(std::move(kind)) {}

    Query& where(std::string_view /*field*/, std::string_view /*op*/, auto&& /*val*/) {
        return *this;
    }

    Query& order_by(std::string_view /*field*/) {
        return *this;
    }

    Query& limit(int32_t n) {
        limit_ = n;
        return *this;
    }

    std::expected<std::vector<T>, cfire::Error> all(Ctx ctx) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            std::string qSpec = "{\"limit\":" + std::to_string(limit_) + "}";
            tako_buf_t res = tako_c_entity_query(ctx.id, kind_.c_str(), qSpec.c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            std::string raw(res.data, res.len);
            tako_c_free(res);
            return std::vector<T>{};
        }
#else
        (void)ctx;
#endif
        std::vector<T> out;
        auto& tbl = detail::local_store().entities[kind_];
        for (const auto& [k, v] : tbl) {
            auto parsed = cfire::json::parse<T>(v);
            if (parsed.has_value()) {
                out.push_back(std::move(*parsed));
                if (limit_ > 0 && (int32_t)out.size() >= limit_) break;
            }
        }
        return out;
    }
};

template <typename T>
class Entity {
public:
    std::string name_;

    explicit Entity(std::string name) : name_(std::move(name)) {}

    std::expected<T, cfire::Error> get(Ctx ctx, std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_entity_get(ctx.id, name_.c_str(), std::string(id).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "not found";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            std::string s(res.data, res.len);
            tako_c_free(res);
            return cfire::json::parse<T>(s);
        }
#else
        (void)ctx;
#endif
        auto& tbl = detail::local_store().entities[name_];
        auto it = tbl.find(std::string(id));
        if (it == tbl.end()) return cfire::make_error(not_found("not found"));
        return cfire::json::parse<T>(it->second);
    }

    std::expected<void, cfire::Error> put(Ctx ctx, const T& val) const {
        std::string json_str = cfire::json::stringify(val);
        std::string id = detail::extract_entity_id<T>(json_str);
        return put(ctx, id, val);
    }

    std::expected<void, cfire::Error> put(Ctx ctx, std::string_view id, const T& val) const {
        std::string json_str = cfire::json::stringify(val);
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_entity_put(ctx.id, name_.c_str(), std::string(id).c_str(), json_str.data(), (int32_t)json_str.size());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        detail::local_store().entities[name_][std::string(id)] = std::move(json_str);
        return {};
    }

    std::expected<void, cfire::Error> delete_entity(Ctx ctx, std::string_view id) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_entity_delete(ctx.id, name_.c_str(), std::string(id).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        detail::local_store().entities[name_].erase(std::string(id));
        return {};
    }

    std::expected<void, cfire::Error> remove(Ctx ctx, std::string_view id) const { return delete_entity(ctx, id); }
    std::expected<void, cfire::Error> del(Ctx ctx, std::string_view id) const { return delete_entity(ctx, id); }

    template <typename Fn>
    std::expected<void, cfire::Error> tx(Ctx ctx, Fn&& fn) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            auto* wrapper = new std::function<std::expected<void, cfire::Error>(Tx<T>)>([&](Tx<T> t) -> std::expected<void, cfire::Error> {
                auto r = fn(t);
                if (!r.has_value()) return std::unexpected(r.error());
                return {};
            });
            auto trampoline = +[](uint64_t tx_id, void* user_data) -> tako_buf_t {
                auto* fp = static_cast<std::function<std::expected<void, cfire::Error>(Tx<T>)>*>(user_data);
                Tx<T> tx_handle{.tx_id = tx_id};
                auto res = (*fp)(tx_handle);
                if (!res.has_value()) {
                    auto msg = res.error().message();
                    char* cErr = (char*)std::malloc(msg.size() + 1);
                    std::memcpy(cErr, msg.data(), msg.size());
                    cErr[msg.size()] = '\0';
                    return tako_buf_t{.data = nullptr, .len = 0, .status = 500, .err = cErr};
                }
                return tako_buf_t{.data = nullptr, .len = 0, .status = 0, .err = nullptr};
            };
            tako_buf_t res = tako_c_entity_tx(ctx.id, name_.c_str(), trampoline, wrapper);
            delete wrapper;
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        Tx<T> tx_handle{.tx_id = 0, .entity_name_ = name_};
        auto r = fn(tx_handle);
        if (!r.has_value()) return std::unexpected(r.error());
        return {};
    }

    Query<T> query() const {
        return Query<T>(name_);
    }
};

class Bucket {
public:
    std::string name_;

    explicit Bucket(std::string name) : name_(std::move(name)) {}

    std::expected<void, cfire::Error> put(Ctx ctx, std::string_view key, std::string_view data, std::string_view content_type = "application/octet-stream") const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_bucket_put(ctx.id, name_.c_str(), std::string(key).c_str(), data.data(), (int32_t)data.size(), std::string(content_type).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        detail::local_store().buckets[name_][std::string(key)] = {std::string(data), std::string(content_type)};
        return {};
    }

    std::expected<void, cfire::Error> put(Ctx ctx, std::string_view key, const std::vector<std::byte>& data, std::string_view content_type = "application/octet-stream") const {
        std::string_view sv(reinterpret_cast<const char*>(data.data()), data.size());
        return put(ctx, key, sv, content_type);
    }

    std::expected<Blob, cfire::Error> get(Ctx ctx, std::string_view key) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_bucket_get(ctx.id, name_.c_str(), std::string(key).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            std::vector<std::byte> bytes(res.len);
            if (res.len > 0 && res.data) {
                std::memcpy(bytes.data(), res.data, res.len);
            }
            tako_c_free(res);
            return Blob{.data = std::move(bytes), .content_type = "application/octet-stream"};
        }
#else
        (void)ctx;
#endif
        auto& b = detail::local_store().buckets[name_];
        auto it = b.find(std::string(key));
        if (it == b.end()) return cfire::make_error(not_found("not found"));
        std::vector<std::byte> bytes(it->second.first.size());
        if (!bytes.empty()) {
            std::memcpy(bytes.data(), it->second.first.data(), bytes.size());
        }
        return Blob{.data = std::move(bytes), .content_type = it->second.second};
    }

    std::expected<void, cfire::Error> remove(Ctx ctx, std::string_view key) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_bucket_delete(ctx.id, name_.c_str(), std::string(key).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        detail::local_store().buckets[name_].erase(std::string(key));
        return {};
    }

    std::expected<std::string, cfire::Error> signed_url(Ctx ctx, std::string_view key, std::string_view method = "GET", int32_t ttl_sec = 900, std::string_view content_type = "") const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_bucket_signed_url(ctx.id, name_.c_str(), std::string(key).c_str(), std::string(method).c_str(), std::string(content_type).c_str(), ttl_sec);
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            std::string s(res.data, res.len);
            tako_c_free(res);
            return s;
        }
#else
        (void)ctx; (void)method; (void)ttl_sec; (void)content_type;
#endif
        return std::format("http://tako.test/_tako/bucket/{}/{}", name_, key);
    }
};

template <typename T>
class Queue {
public:
    std::string name_;

    explicit Queue(std::string name) : name_(std::move(name)) {}

    std::expected<void, cfire::Error> publish(Ctx ctx, const T& val, int32_t delay_sec = 0) const {
        std::string json_str = cfire::json::stringify(val);
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_queue_publish(ctx.id, name_.c_str(), json_str.data(), (int32_t)json_str.size(), delay_sec);
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx; (void)delay_sec;
#endif
        detail::local_store().queues[name_].push_back(std::move(json_str));
        return {};
    }
};

template <typename T>
class Topic {
public:
    std::string name_;

    explicit Topic(std::string name) : name_(std::move(name)) {}

    std::expected<void, cfire::Error> publish(Ctx ctx, const T& val) const {
        std::string json_str = cfire::json::stringify(val);
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_topic_publish(ctx.id, name_.c_str(), json_str.data(), (int32_t)json_str.size());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        detail::local_store().queues[name_].push_back(std::move(json_str));
        return {};
    }
};

template <typename T>
class Job {
public:
    std::string name_;

    explicit Job(std::string name) : name_(std::move(name)) {}

    std::expected<std::string, cfire::Error> start(Ctx ctx, const T& params) const {
        (void)ctx;
        std::string json_str = cfire::json::stringify(params);
        detail::local_store().queues[name_].push_back(std::move(json_str));
        return std::string("job_exec_1");
    }
};

template <typename T>
class Cache {
public:
    std::string name_;

    explicit Cache(std::string name) : name_(std::move(name)) {}

    std::optional<T> get(Ctx ctx, std::string_view key) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_cache_get(ctx.id, name_.c_str(), std::string(key).c_str());
            if (res.status != 0 || !res.data) {
                tako_c_free(res);
                return std::nullopt;
            }
            std::string s(res.data, res.len);
            tako_c_free(res);
            auto parsed = cfire::json::parse<T>(s);
            if (!parsed.has_value()) return std::nullopt;
            return *parsed;
        }
#else
        (void)ctx;
#endif
        auto& c = detail::local_store().caches[name_];
        auto it = c.find(std::string(key));
        if (it == c.end()) return std::nullopt;
        auto parsed = cfire::json::parse<T>(it->second);
        if (!parsed.has_value()) return std::nullopt;
        return *parsed;
    }

    std::expected<void, cfire::Error> set(Ctx ctx, std::string_view key, const T& val, int32_t ttl_sec = 300) const {
        std::string json_str = cfire::json::stringify(val);
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_cache_set(ctx.id, name_.c_str(), std::string(key).c_str(), json_str.data(), (int32_t)json_str.size(), ttl_sec);
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx; (void)ttl_sec;
#endif
        detail::local_store().caches[name_][std::string(key)] = std::move(json_str);
        return {};
    }

    std::expected<void, cfire::Error> remove(Ctx ctx, std::string_view key) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_cache_delete(ctx.id, name_.c_str(), std::string(key).c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "error";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
            return {};
        }
#else
        (void)ctx;
#endif
        detail::local_store().caches[name_].erase(std::string(key));
        return {};
    }
};

class Lock {
public:
    std::string name_;
    explicit Lock(std::string name) : name_(std::move(name)) {}

    template <typename Fn>
    std::expected<void, cfire::Error> with_lease(Ctx ctx, int32_t ttl_sec, Fn&& fn) const {
        (void)ctx; (void)ttl_sec;
        auto r = fn();
        if (!r.has_value()) return std::unexpected(r.error());
        return {};
    }
};

class Secret {
public:
    std::string name_;
    explicit Secret(std::string name) : name_(std::move(name)) {}

    std::expected<std::string, cfire::Error> get(Ctx ctx = {}) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_buf_t res = tako_c_secret_get(ctx.id, name_.c_str());
            if (res.status != 0) {
                std::string err = res.err ? res.err : "secret not set";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            std::string s(res.data, res.len);
            tako_c_free(res);
            return s;
        }
#else
        (void)ctx;
#endif
        auto& secs = detail::local_store().secrets;
        if (auto it = secs.find(name_); it != secs.end()) return it->second;
        if (const char* env_v = std::getenv(name_.c_str())) return std::string(env_v);
        return cfire::make_error(std::format("secret \"{}\" is not set", name_));
    }

    std::expected<std::string, cfire::Error> value() const { return get(); }
};

class Counter {
public:
    std::string name_;
    explicit Counter(std::string name) : name_(std::move(name)) {}

    void inc(Ctx ctx, int64_t delta = 1) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_c_metric_inc(ctx.id, name_.c_str(), delta);
            return;
        }
#else
        (void)ctx;
#endif
        detail::local_store().counters[name_] += delta;
    }

    void inc(int64_t delta = 1) const {
        detail::local_store().counters[name_] += delta;
    }
};

class Histogram {
public:
    std::string name_;
    explicit Histogram(std::string name) : name_(std::move(name)) {}

    void observe(Ctx ctx, double val) const {
#ifdef CFIRE_HAS_LIBTAKO
        if (ctx.id != 0) {
            tako_c_metric_observe(ctx.id, name_.c_str(), val);
            return;
        }
#else
        (void)ctx;
#endif
        detail::local_store().histograms[name_].push_back(val);
    }

    void observe(double val) const {
        detail::local_store().histograms[name_].push_back(val);
    }
};

struct Site {
    std::string dir;
    SiteOpts opts;
};

class TestApp {
public:
    uint64_t app_id{0};

    TestApp() {
        detail::local_store().reset();
#ifdef CFIRE_HAS_LIBTAKO
        app_id = tako_c_testapp_new();
#endif
    }

    ~TestApp() {
        close();
    }

    TestApp(const TestApp&) = delete;
    TestApp& operator=(const TestApp&) = delete;
    TestApp(TestApp&& other) noexcept : app_id(other.app_id) { other.app_id = 0; }
    TestApp& operator=(TestApp&& other) noexcept {
        if (this != &other) {
            close();
            app_id = other.app_id;
            other.app_id = 0;
        }
        return *this;
    }

    Ctx ctx() const {
        return Ctx{.id = 0};
    }

    Ctx ctx(User u) const {
        return Ctx{.id = 0, .current_user = std::move(u)};
    }

    User sign_in(std::string_view name) const {
        std::string n(name);
        return User{.id = n, .email = n + "@example.test", .name = n, .provider = "test", .roles = {}};
    }

    void set_secret(std::string_view name, std::string_view val) const {
        detail::local_store().secrets[std::string(name)] = std::string(val);
    }

    std::expected<void, cfire::Error> drain() const {
#ifdef CFIRE_HAS_LIBTAKO
        if (app_id != 0) {
            tako_buf_t res = tako_c_testapp_drain(app_id);
            if (res.status != 0) {
                std::string err = res.err ? res.err : "drain failed";
                tako_c_free(res);
                return cfire::make_error(err);
            }
            tako_c_free(res);
        }
#endif
        auto& qs = detail::local_store().queues;
        auto& ws = detail::local_store().workers;
        for (auto& [qname, msgs] : qs) {
            if (auto wit = ws.find(qname); wit != ws.end()) {
                for (const auto& m : msgs) {
                    for (const auto& wfn : wit->second) {
                        auto r = wfn(0, m);
                        if (!r.has_value()) return std::unexpected(r.error());
                    }
                }
            }
            msgs.clear();
        }
        return {};
    }

    std::expected<void, cfire::Error> drain_queues() const { return drain(); }

    void close() {
#ifdef CFIRE_HAS_LIBTAKO
        if (app_id != 0) {
            tako_c_testapp_close(app_id);
            app_id = 0;
        }
#endif
    }
};

inline TestApp new_test_app() {
    return TestApp{};
}

// Declarations constructors
template <typename T>
inline Entity<T> entity(std::string_view name) { return Entity<T>(std::string(name)); }

inline Bucket bucket(std::string_view name) { return Bucket(std::string(name)); }

struct DiskOpts {
    bool read_only = false;
    bool ephemeral = false;
    std::string size_limit = "";
};

class Disk {
public:
    std::string name_;
    DiskOpts opts_;

    explicit Disk(std::string name, DiskOpts opts = {}) : name_(std::move(name)), opts_(std::move(opts)) {}

    std::string path(Ctx /*ctx*/, std::string_view rel) const {
        return "/mnt/tako/" + name_ + "/" + std::string(rel);
    }

    std::expected<std::string, cfire::Error> read_file(Ctx /*ctx*/, std::string_view rel) const {
        auto& b = detail::local_store().buckets["disk:" + name_];
        auto it = b.find(std::string(rel));
        if (it == b.end()) return cfire::make_error(not_found("not found"));
        return it->second.first;
    }

    std::expected<void, cfire::Error> write_file(Ctx /*ctx*/, std::string_view rel, std::string_view data) const {
        if (opts_.read_only) return cfire::make_error("write to read-only disk");
        detail::local_store().buckets["disk:" + name_][std::string(rel)] = {std::string(data), "application/octet-stream"};
        return {};
    }

    std::expected<void, cfire::Error> remove(Ctx /*ctx*/, std::string_view rel) const {
        if (opts_.read_only) return cfire::make_error("write to read-only disk");
        detail::local_store().buckets["disk:" + name_].erase(std::string(rel));
        return {};
    }
};

inline Disk disk(std::string_view name, DiskOpts opts = {}) { return Disk(std::string(name), std::move(opts)); }

template <typename T>
inline Queue<T> queue(std::string_view name, QueueOpts /*opts*/ = {}) { return Queue<T>(std::string(name)); }

template <typename T>
inline Topic<T> topic(std::string_view name) { return Topic<T>(std::string(name)); }

template <typename T>
inline Job<T> job(std::string_view name) { return Job<T>(std::string(name)); }

template <typename T>
inline Cache<T> cache(std::string_view name, int32_t /*ttl_sec*/ = 300) { return Cache<T>(std::string(name)); }

inline Lock lock(std::string_view name) { return Lock(std::string(name)); }

inline Secret secret(std::string_view name) { return Secret(std::string(name)); }

inline Counter counter(std::string_view name) { return Counter(std::string(name)); }

inline Histogram histogram(std::string_view name) { return Histogram(std::string(name)); }

inline Site site(std::string_view dir, SiteOpts opts = {}) { return Site{.dir = std::string(dir), .opts = std::move(opts)}; }

template <typename Fn>
inline auto api(std::string_view /*route*/, Fn&& fn, RouteOpts /*opts*/ = {}) { return std::forward<Fn>(fn); }

template <typename Fn>
inline auto page(std::string_view /*route*/, Fn&& fn, RouteOpts /*opts*/ = {}) { return std::forward<Fn>(fn); }

template <typename Q, typename Fn>
inline auto worker(const Q& q, Fn&& fn) {
    if constexpr (requires { q.name_; }) {
        using MsgT = decltype([]<typename T>(const Queue<T>&) -> T { return T{}; }(q));
        detail::local_store().workers[q.name_].push_back(
            [f = fn](uint64_t cid, std::string_view pay) -> std::expected<void, cfire::Error> {
                auto parsed = cfire::json::parse<MsgT>(pay);
                if (!parsed.has_value()) return std::unexpected(parsed.error());
                auto r = f(Ctx{.id = cid}, *parsed);
                if (!r.has_value()) return std::unexpected(r.error());
                return {};
            }
        );
    }
    return std::forward<Fn>(fn);
}

template <typename T, typename Fn>
inline auto subscribe(const T& t, Fn&& fn) {
    if constexpr (requires { t.name_; }) {
        using MsgT = decltype([]<typename U>(const Topic<U>&) -> U { return U{}; }(t));
        detail::local_store().workers[t.name_].push_back(
            [f = fn](uint64_t cid, std::string_view pay) -> std::expected<void, cfire::Error> {
                auto parsed = cfire::json::parse<MsgT>(pay);
                if (!parsed.has_value()) return std::unexpected(parsed.error());
                auto r = f(Ctx{.id = cid}, *parsed);
                if (!r.has_value()) return std::unexpected(r.error());
                return {};
            }
        );
    }
    return std::forward<Fn>(fn);
}

template <typename B, typename Fn>
inline auto subscribe_bucket(const B& /*b*/, Fn&& fn) { return std::forward<Fn>(fn); }

template <typename Fn>
inline auto cron(std::string_view /*sched*/, Fn&& fn, CronOpts /*opts*/ = {}) { return std::forward<Fn>(fn); }

template <typename J, typename Fn>
inline auto runner(const J& /*j*/, Fn&& fn) { return std::forward<Fn>(fn); }

template <typename Fn>
inline auto singleton(Fn&& fn) { return std::forward<Fn>(fn); }

inline ServiceOpts service(std::string_view /*name*/, ServiceOpts opts = {}) { return opts; }

inline std::string role(std::string_view r) { return std::string(r); }
inline std::string require_role(std::string_view r) { return std::string(r); }
inline std::string auth(auto&&... /*providers*/) { return "auth"; }

} // namespace cfire::tako

namespace cfire::core {
namespace tako = ::cfire::tako;
} // namespace cfire::core

namespace tako = ::cfire::tako;
