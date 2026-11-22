// TownCrier standalone Whisper + Vulkan/CPU stdio helper worker.
// Compiled from pinned ggml-org/whisper.cpp source during build.sh (no runtime binary downloads).

#include "whisper.h"
#include "ggml-backend.h"

#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

#ifdef _WIN32
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <shellapi.h>
#include <fcntl.h>
#include <io.h>
#endif

namespace {

constexpr uint32_t kFrameMagic = 0x54435753u; // "TCWS"
constexpr uint32_t kMaxSamples = 16000u * 60u; // 60 seconds max per VAD segment

void stderr_log_callback(enum ggml_log_level level, const char * text, void * /*user_data*/) {
    if (level >= GGML_LOG_LEVEL_WARN && text != nullptr) {
        std::fputs(text, stderr);
    }
}

std::string json_escape(const std::string & s) {
    std::string out;
    out.reserve(s.size() + 8);
    for (unsigned char c : s) {
        switch (c) {
            case '"':  out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\b': out += "\\b";  break;
            case '\f': out += "\\f";  break;
            case '\n': out += "\\n";  break;
            case '\r': out += "\\r";  break;
            case '\t': out += "\\t";  break;
            default:
                if (c < 0x20) {
                    char buf[8];
                    std::snprintf(buf, sizeof(buf), "\\u%04x", static_cast<unsigned int>(c));
                    out += buf;
                } else {
                    out.push_back(static_cast<char>(c));
                }
                break;
        }
    }
    return out;
}

bool read_exact(void * dst, size_t n, bool * clean_eof = nullptr) {
    if (clean_eof != nullptr) {
        *clean_eof = false;
    }
    unsigned char * p = static_cast<unsigned char *>(dst);
    size_t total = 0;
    while (total < n) {
        size_t got = std::fread(p + total, 1, n - total, stdin);
        if (got == 0) {
            if (total == 0 && std::feof(stdin) && clean_eof != nullptr) {
                *clean_eof = true;
            }
            return false;
        }
        total += got;
    }
    return true;
}

bool write_json_frame(const std::string & json_payload) {
    uint32_t len = static_cast<uint32_t>(json_payload.size());
    if (std::fwrite(&len, sizeof(len), 1, stdout) != 1) {
        return false;
    }
    if (len > 0 && std::fwrite(json_payload.data(), 1, len, stdout) != len) {
        return false;
    }
    return std::fflush(stdout) == 0;
}

int parse_vulkan_index(const char * dev_name) {
    if (dev_name == nullptr) {
        return -1;
    }
    if (std::strncmp(dev_name, "Vulkan", 6) == 0 && dev_name[6] >= '0' && dev_name[6] <= '9') {
        return std::atoi(dev_name + 6);
    }
    return -1;
}

struct GPUDeviceEntry {
    int         gpu_index;
    int         vk_index;
    std::string name;
    std::string description;
    std::string type; // "dgpu" or "igpu"
    uint64_t    memory_free_mb;
    uint64_t    memory_total_mb;
};

std::vector<GPUDeviceEntry> enumerate_gpu_devices() {
    std::vector<GPUDeviceEntry> out;
    int gpu_cnt = 0;
    size_t total_devs = ggml_backend_dev_count();
    for (size_t i = 0; i < total_devs; ++i) {
        ggml_backend_dev_t dev = ggml_backend_dev_get(i);
        if (dev == nullptr) {
            continue;
        }
        enum ggml_backend_dev_type dtype = ggml_backend_dev_type(dev);
        if (dtype != GGML_BACKEND_DEVICE_TYPE_GPU && dtype != GGML_BACKEND_DEVICE_TYPE_IGPU) {
            continue;
        }
        struct ggml_backend_dev_props props {};
        ggml_backend_dev_get_props(dev, &props);

        GPUDeviceEntry entry;
        entry.gpu_index = gpu_cnt++;
        entry.name = props.name ? props.name : "";
        entry.vk_index = parse_vulkan_index(props.name);
        entry.description = props.description ? props.description : entry.name;
        entry.type = (dtype == GGML_BACKEND_DEVICE_TYPE_IGPU) ? "igpu" : "dgpu";
        entry.memory_free_mb = static_cast<uint64_t>(props.memory_free / (1024ull * 1024ull));
        entry.memory_total_mb = static_cast<uint64_t>(props.memory_total / (1024ull * 1024ull));
        out.push_back(std::move(entry));
    }
    return out;
}

int run_list_devices() {
    ggml_log_set(stderr_log_callback, nullptr);
    whisper_log_set(stderr_log_callback, nullptr);

    auto devs = enumerate_gpu_devices();
    std::string json = "[";
    for (size_t i = 0; i < devs.size(); ++i) {
        if (i > 0) {
            json += ",";
        }
        const auto & d = devs[i];
        char num_buf[192];
        std::snprintf(
            num_buf,
            sizeof(num_buf),
            "{\"gpu_index\":%d,\"vk_index\":%d,",
            d.gpu_index,
            d.vk_index
        );
        json += num_buf;
        json += "\"name\":\"" + json_escape(d.name) + "\",";
        json += "\"description\":\"" + json_escape(d.description) + "\",";
        json += "\"type\":\"" + json_escape(d.type) + "\",";
        std::snprintf(
            num_buf,
            sizeof(num_buf),
            "\"memory_free_mb\":%llu,\"memory_total_mb\":%llu}",
            static_cast<unsigned long long>(d.memory_free_mb),
            static_cast<unsigned long long>(d.memory_total_mb)
        );
        json += num_buf;
    }
    json += "]\n";
    std::fwrite(json.data(), 1, json.size(), stdout);
    std::fflush(stdout);
    return 0;
}

} // namespace

int main(int argc, char ** argv) {
    std::vector<std::string> args;
#ifdef _WIN32
    int wargc = 0;
    LPWSTR * wargv = CommandLineToArgvW(GetCommandLineW(), &wargc);
    if (wargv != nullptr) {
        args.reserve(static_cast<size_t>(wargc));
        for (int i = 0; i < wargc; ++i) {
            int needed = WideCharToMultiByte(CP_UTF8, 0, wargv[i], -1, nullptr, 0, nullptr, nullptr);
            if (needed > 1) {
                std::string s(static_cast<size_t>(needed - 1), '\0');
                WideCharToMultiByte(CP_UTF8, 0, wargv[i], -1, &s[0], needed, nullptr, nullptr);
                args.push_back(std::move(s));
            } else {
                args.emplace_back();
            }
        }
        LocalFree(wargv);
    }
#endif
    if (args.empty()) {
        for (int i = 0; i < argc; ++i) {
            args.emplace_back(argv[i] ? argv[i] : "");
        }
    }

    bool list_devices = false;
    bool no_gpu = false;
    int gpu_device = 0;
    int default_threads = 2;
    std::string model_path;

    for (size_t i = 1; i < args.size(); ++i) {
        const std::string & a = args[i];
        if (a == "--list-devices") {
            list_devices = true;
        } else if (a == "--no-gpu") {
            no_gpu = true;
        } else if (a == "--gpu-device" && i + 1 < args.size()) {
            gpu_device = std::atoi(args[++i].c_str());
        } else if (a == "--threads" && i + 1 < args.size()) {
            default_threads = std::atoi(args[++i].c_str());
        } else if (a == "--model" && i + 1 < args.size()) {
            model_path = args[++i];
        }
    }

    if (list_devices) {
        return run_list_devices();
    }

#ifdef _WIN32
    _setmode(_fileno(stdin), _O_BINARY);
    _setmode(_fileno(stdout), _O_BINARY);
#endif

    ggml_log_set(stderr_log_callback, nullptr);
    whisper_log_set(stderr_log_callback, nullptr);

    if (model_path.empty()) {
        write_json_frame("{\"ok\":false,\"error\":\"missing --model path\"}");
        return 2;
    }
    if (default_threads < 1) {
        default_threads = 2;
    }

    auto devs = enumerate_gpu_devices();
    bool want_gpu = !no_gpu && !devs.empty();
    std::string active_provider = "cpu";

    struct whisper_context_params cparams = whisper_context_default_params();
    cparams.use_gpu = want_gpu;
    cparams.gpu_device = gpu_device;
    cparams.flash_attn = true;

    struct whisper_context * ctx = whisper_init_from_file_with_params(model_path.c_str(), cparams);
    if (ctx == nullptr && want_gpu) {
        std::fprintf(stderr, "[towncrier-whisper] GPU init failed for device %d, falling back to CPU...\n", gpu_device);
        cparams.use_gpu = false;
        ctx = whisper_init_from_file_with_params(model_path.c_str(), cparams);
        want_gpu = false;
    }
    if (ctx == nullptr) {
        write_json_frame("{\"ok\":false,\"error\":\"failed to initialize whisper model\"}");
        return 3;
    }

    if (want_gpu) {
        for (const auto & d : devs) {
            if (d.gpu_index == gpu_device) {
                active_provider = "vulkan:" + d.description + " (" + d.type + ")";
                break;
            }
        }
        if (active_provider == "cpu" && !devs.empty()) {
            active_provider = "vulkan:" + devs[0].description + " (" + devs[0].type + ")";
        }
    }

    std::string ready_msg = "{\"ok\":true,\"ready\":true,\"provider\":\"" + json_escape(active_provider) + "\"}";
    if (!write_json_frame(ready_msg)) {
        whisper_free(ctx);
        return 4;
    }

    std::vector<float> pcm;
    for (;;) {
        bool clean_eof = false;
        uint32_t magic = 0;
        if (!read_exact(&magic, sizeof(magic), &clean_eof)) {
            whisper_free(ctx);
            return clean_eof ? 0 : 1;
        }
        if (magic != kFrameMagic) {
            write_json_frame("{\"ok\":false,\"error\":\"protocol frame magic mismatch\"}");
            whisper_free(ctx);
            return 5;
        }

        uint32_t n_samples = 0;
        uint32_t lang_len = 0;
        int32_t req_threads = 0;
        if (!read_exact(&n_samples, sizeof(n_samples)) ||
            !read_exact(&lang_len, sizeof(lang_len)) ||
            !read_exact(&req_threads, sizeof(req_threads))) {
            whisper_free(ctx);
            return 1;
        }
        if (n_samples > kMaxSamples || lang_len > 64) {
            write_json_frame("{\"ok\":false,\"error\":\"frame bounds exceeded\"}");
            whisper_free(ctx);
            return 6;
        }

        std::string lang;
        if (lang_len > 0) {
            lang.resize(lang_len);
            if (!read_exact(&lang[0], lang_len)) {
                whisper_free(ctx);
                return 1;
            }
        }

        pcm.resize(n_samples);
        if (n_samples > 0) {
            if (!read_exact(pcm.data(), static_cast<size_t>(n_samples) * sizeof(float))) {
                whisper_free(ctx);
                return 1;
            }
        }

        if (n_samples == 0) {
            if (!write_json_frame("{\"ok\":true,\"text\":\"\",\"lang\":\"\",\"decode_ms\":0}")) {
                break;
            }
            continue;
        }

        struct whisper_full_params wparams = whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
        wparams.n_threads = (req_threads > 0) ? req_threads : default_threads;
        wparams.translate = false;
        wparams.no_context = true;
        wparams.no_timestamps = true;
        wparams.single_segment = true;
        wparams.print_special = false;
        wparams.print_progress = false;
        wparams.print_realtime = false;
        wparams.print_timestamps = false;
        wparams.suppress_blank = true;
        wparams.suppress_nst = true;
        wparams.temperature_inc = 0.0f;
        wparams.max_tokens = 64;
        wparams.greedy.best_of = 1;

        if (!lang.empty() && lang != "auto") {
            wparams.language = lang.c_str();
            wparams.detect_language = false;
        } else {
            wparams.language = "auto";
        }

        auto t0 = std::chrono::steady_clock::now();
        int rc = whisper_full(ctx, wparams, pcm.data(), static_cast<int>(n_samples));
        auto t1 = std::chrono::steady_clock::now();
        long long decode_ms = std::chrono::duration_cast<std::chrono::milliseconds>(t1 - t0).count();

        if (rc != 0) {
            if (!write_json_frame("{\"ok\":false,\"error\":\"whisper_full decode failed\"}")) {
                break;
            }
            continue;
        }

        std::string full_text;
        int n_seg = whisper_full_n_segments(ctx);
        for (int s = 0; s < n_seg; ++s) {
            const char * seg_txt = whisper_full_get_segment_text(ctx, s);
            if (seg_txt != nullptr) {
                full_text += seg_txt;
            }
        }

        std::string detected_lang;
        int lang_id = whisper_full_lang_id(ctx);
        if (lang_id >= 0) {
            const char * lstr = whisper_lang_str(lang_id);
            if (lstr != nullptr) {
                detected_lang = lstr;
            }
        }

        char ms_buf[64];
        std::snprintf(ms_buf, sizeof(ms_buf), "%lld", decode_ms);
        std::string resp = "{\"ok\":true,\"text\":\"" + json_escape(full_text) +
                           "\",\"lang\":\"" + json_escape(detected_lang) +
                           "\",\"decode_ms\":" + ms_buf + "}";
        if (!write_json_frame(resp)) {
            break;
        }
    }

    whisper_free(ctx);
    return 0;
}
