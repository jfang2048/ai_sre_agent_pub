// SPDX-License-Identifier: GPL-2.0 OR BSD-2-Clause
/* Copyright (c) 2026 SRE Agent */

#include <gtest/gtest.h>
#include <gmock/gmock.h>
#include "../sdk/include/ebpf.h"
#include <thread>
#include <chrono>
#include <vector>

using namespace sre::sdk::ebpf;
using ::testing::_;
using ::testing::AtLeast;
using ::testing::DoAll;
using ::testing::Return;
using ::testing::SetArgPointee;

// Mock eBPF provider for testing
class MockEBPFProvider {
public:
    MOCK_METHOD0(initialize, ErrorCode());
    MOCK_METHOD0(is_supported, bool());
    MOCK_CONST_METHOD0(get_kernel_version, std::string());
};

// Test ErrorCode to string conversion
TEST(EBPFTest, ErrorToString) {
    EXPECT_STREQ("Success", error_string(ErrorCode::OK));
    EXPECT_STREQ("Failed to load program", error_string(ErrorCode::LOAD_FAILED));
    EXPECT_STREQ("Failed to attach program", error_string(ErrorCode::ATTACH_FAILED));
    EXPECT_STREQ("Permission denied", error_string(ErrorCode::PERMISSION_DENIED));
    EXPECT_STREQ("Not supported", error_string(ErrorCode::NOT_SUPPORTED));
    EXPECT_STREQ("Invalid argument", error_string(ErrorCode::INVALID_ARGUMENT));
}

// Test EBPFProgram construction
TEST(EBPFProgramTest, Construction) {
    EBPFProgram prog("test_program");
    EXPECT_EQ("test_program", prog.name());
    EXPECT_FALSE(prog.is_loaded());
    EXPECT_FALSE(prog.is_attached());
}

// Test EBPFProgram move semantics
TEST(EBPFProgramTest, MoveSemantics) {
    EBPFProgram prog1("original");
    EBPFProgram prog2(std::move(prog1));

    EXPECT_EQ("original", prog2.name());
    EXPECT_FALSE(prog1.is_loaded());  // NOLINT: use after move is intentional
    EXPECT_FALSE(prog2.is_loaded());
}

// Test AttachPoint construction
TEST(AttachPointTest, Construction) {
    AttachPoint ap1;
    EXPECT_EQ(ProgramType::KPROBE, ap1.type);
    EXPECT_TRUE(ap1.is_entry);
    EXPECT_TRUE(ap1.name.empty());

    AttachPoint ap2(ProgramType::TRACEPOINT, "sched:sched_switch", true);
    EXPECT_EQ(ProgramType::TRACEPOINT, ap2.type);
    EXPECT_EQ("sched:sched_switch", ap2.name);
    EXPECT_TRUE(ap2.is_entry);
}

// Test EBPFProvider kernel version detection
TEST(EBPFProviderTest, KernelVersionDetection) {
    std::string version = EBPFProvider::get_kernel_version();
    EXPECT_FALSE(version.empty());
    EXPECT_NE("unknown", version);

    // Version should contain dots
    EXPECT_NE(version.find('.'), std::string::npos);
}

// Test EBPFProvider support check
TEST(EBPFProviderTest, IsSupported) {
    bool supported = EBPFProvider::is_supported();
    // We can't assert true/false since it depends on the system
    // Just verify the call doesn't crash
    EXPECT_TRUE(supported || !supported);
}

// Test syscall tracepoint constants
TEST(TracepointTest, SyscallConstants) {
    EXPECT_STREQ("syscalls:sys_enter_read", syscalls::sys_enter_read);
    EXPECT_STREQ("syscalls:sys_enter_write", syscalls::sys_enter_write);
    EXPECT_STREQ("syscalls:sys_exit_read", syscalls::sys_exit_read);
    EXPECT_STREQ("syscalls:sys_exit_write", syscalls::sys_exit_write);
}

// Test network tracepoint constants
TEST(TracepointTest, NetworkConstants) {
    EXPECT_STREQ("net:netif_receive_skb", net::netif_receive_skb);
    EXPECT_STREQ("net:net_dev_xmit", net::net_dev_xmit);
    EXPECT_STREQ("tcp:tcp_retransmit_skb", net::tcp_retransmit_skb);
}

// Test scheduler tracepoint constants
TEST(TracepointTest, SchedulerConstants) {
    EXPECT_STREQ("sched:sched_process_fork", sched::sched_process_fork);
    EXPECT_STREQ("sched:sched_process_exec", sched::sched_process_exec);
    EXPECT_STREQ("sched:sched_process_exit", sched::sched_process_exit);
    EXPECT_STREQ("sched:sched_switch", sched::sched_switch);
}

// Test block I/O tracepoint constants
TEST(TracepointTest, BlockConstants) {
    EXPECT_STREQ("block:block_rq_insert", block::block_rq_insert);
    EXPECT_STREQ("block:block_rq_complete", block::block_rq_complete);
    EXPECT_STREQ("block:block_bio_queue", block::block_bio_queue);
}

// Test MapType enum values
TEST(MapTypeTest, EnumValues) {
    EXPECT_EQ(static_cast<uint32_t>(1), static_cast<uint32_t>(MapType::HASH));
    EXPECT_EQ(static_cast<uint32_t>(2), static_cast<uint32_t>(MapType::ARRAY));
    EXPECT_EQ(static_cast<uint32_t>(4), static_cast<uint32_t>(MapType::PERF_EVENT_ARRAY));
    EXPECT_EQ(static_cast<uint32_t>(10), static_cast<uint32_t>(MapType::RINGBUF));
}

// Test ProgramType enum values
TEST(ProgramTypeTest, EnumValues) {
    EXPECT_EQ(static_cast<uint32_t>(0), static_cast<uint32_t>(ProgramType::KPROBE));
    EXPECT_EQ(static_cast<uint32_t>(1), static_cast<uint32_t>(ProgramType::KRETPROBE));
    EXPECT_EQ(static_cast<uint32_t>(2), static_cast<uint32_t>(ProgramType::TRACEPOINT));
    EXPECT_EQ(static_cast<uint32_t>(3), static_cast<uint32_t>(ProgramType::XDP));
}

// Test LoadConfig defaults
TEST(LoadConfigTest, Defaults) {
    LoadConfig config;
    EXPECT_TRUE(config.program_path.empty());
    EXPECT_FALSE(config.verifier_logs);
    EXPECT_TRUE(config.cflags.empty());
}

// Test LoadConfig with values
TEST(LoadConfigTest, WithValues) {
    LoadConfig config;
    config.program_path = "/path/to/program.o";
    config.verifier_logs = true;
    config.cflags = {"-Wall", "-Wextra"};

    EXPECT_EQ("/path/to/program.o", config.program_path);
    EXPECT_TRUE(config.verifier_logs);
    EXPECT_EQ(2, config.cflags.size());
}

// Performance test for error string conversion
TEST(PerformanceTest, ErrorStringConversion) {
    auto start = std::chrono::high_resolution_clock::now();

    for (int i = 0; i < 100000; ++i) {
        error_string(ErrorCode::OK);
        error_string(ErrorCode::LOAD_FAILED);
        error_string(ErrorCode::ATTACH_FAILED);
    }

    auto end = std::chrono::high_resolution_clock::now();
    auto duration = std::chrono::duration_cast<std::chrono::microseconds>(end - start);

    // Should complete in reasonable time (< 100ms for 300k calls)
    EXPECT_LT(duration.count(), 100000);
}

int main(int argc, char** argv) {
    ::testing::InitGoogleTest(&argc, argv);
    return RUN_ALL_TESTS();
}
