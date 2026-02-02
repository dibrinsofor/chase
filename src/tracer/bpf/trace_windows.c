//go:build ignore

#include "ebpf_helpers.h"

#define MAX_PATH_LEN 256
#define MAX_ENTRIES 10240

#define OP_READ  0
#define OP_WRITE 1
#define OP_OPEN  2

struct trace_event {
    __u32 pid;
    __u32 tid;
    __u64 timestamp;
    __u32 flags;
    __u8  op;
    __u8  padding[3];
    char  path[MAX_PATH_LEN];
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, MAX_ENTRIES * sizeof(struct trace_event));
} events SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_ENTRIES);
    __type(key, __u32);
    __type(value, __u32);
} target_pids SEC(".maps");

volatile const __u32 filter_pid = 0;
volatile const bool follow_children = true;

static __always_inline bool should_trace(__u32 pid) {
    if (filter_pid == 0) {
        return true;
    }
    if (pid == filter_pid) {
        return true;
    }
    if (follow_children) {
        __u32 *found = bpf_map_lookup_elem(&target_pids, &pid);
        if (found) {
            return true;
        }
    }
    return false;
}

SEC("file_create")
int trace_file_create(void *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;
    __u32 tid = (__u32)pid_tgid;

    if (!should_trace(pid)) {
        return 0;
    }

    struct trace_event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e) {
        return 0;
    }

    e->pid = pid;
    e->tid = tid;
    e->timestamp = bpf_ktime_get_ns();
    e->flags = 0;
    e->op = OP_OPEN;

    bpf_ringbuf_submit(e, 0);
    return 0;
}

SEC("process_create")
int trace_process_create(void *ctx) {
    if (!follow_children) {
        return 0;
    }

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 parent_pid = pid_tgid >> 32;

    if (!should_trace(parent_pid)) {
        return 0;
    }

    return 0;
}

char LICENSE[] SEC("license") = "MIT";
