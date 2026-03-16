#include <linux/types.h>

// Mock definitions for syntax check only - Real compilation needs kernel
// headers
typedef __u32 u32;
typedef __u64 u64;
typedef __u16 u16;

struct sock {
  struct {
    int skc_daddr;
    int skc_rcv_saddr;
    short skc_num;
    short skc_dport;
  } __sk_common;
};

struct pt_regs {};

// #include <uapi/linux/ptrace.h>
// #include <net/sock.h>
// #include <bcc/proto.h>

// Define map to store active connections
struct key_t {
  u32 src_ip;
  u32 dst_ip;
  u16 src_port;
  u16 dst_port;
};

struct value_t {
  u64 packets;
  u64 bytes;
  u64 ts_start;
  u64 ts_last;
};

// Mock BPF_HASH
#define BPF_HASH(name, key, value)                                             \
  struct {                                                                     \
  } name

BPF_HASH(connections, struct key_t, struct value_t);

// Trace TCP connect (Outbound)
int trace_connect(struct pt_regs *ctx, struct sock *sk) {
  // u32 pid = bpf_get_current_pid_tgid() >> 32; // Function not defined in mock

  struct key_t key = {};

  // Read socket details
  u32 daddr = sk->__sk_common.skc_daddr;
  u32 saddr = sk->__sk_common.skc_rcv_saddr;
  u16 lport = sk->__sk_common.skc_num;

  key.src_ip = saddr;
  key.dst_ip = daddr;
  key.src_port = lport;
  key.dst_port = sk->__sk_common.skc_dport; // Network byte order

  struct value_t val = {};
  // val.ts_start = bpf_ktime_get_ns();
  val.ts_last = val.ts_start;
  val.packets = 1;

  // connections.update(&key, &val);

  return 0;
}

// Trace TCP accept (Inbound)
int trace_accept(struct pt_regs *ctx, struct sock *sk) {
  // Similar logic for inbound
  return 0;
}
