#include "subprocess.h"

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/wait.h>
#include <unistd.h>

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <string>
#include <vector>

#include "posix_raii.h"

namespace probe_core {

std::string RunCommandCapture(const std::vector<std::string>& argv, int timeout_ms) {
  if (argv.empty() || argv[0].empty()) return {};
  if (timeout_ms <= 0) timeout_ms = kDefaultExternalCommandTimeoutMs;

  int pipe_fds[2] = {-1, -1};
#ifdef O_CLOEXEC
  if (pipe2(pipe_fds, O_CLOEXEC) != 0) return {};
#else
  if (pipe(pipe_fds) != 0) return {};
#endif
  ScopedFD read_end(pipe_fds[0]);
  ScopedFD write_end(pipe_fds[1]);

  const pid_t pid = fork();
  if (pid < 0) return {};

  if (pid == 0) {
    if (dup2(write_end.get(), STDOUT_FILENO) < 0) _exit(127);
    ScopedFD devnull(open("/dev/null", O_WRONLY | O_CLOEXEC));
    if (devnull.valid()) {
      if (dup2(devnull.get(), STDERR_FILENO) < 0) _exit(127);
    }

    read_end.reset();
    write_end.reset();

    std::vector<char*> exec_argv;
    exec_argv.reserve(argv.size() + 1);
    for (const auto& arg : argv) {
      exec_argv.push_back(const_cast<char*>(arg.c_str()));
    }
    exec_argv.push_back(nullptr);

    execvp(exec_argv[0], exec_argv.data());
    _exit(127);
  }

  write_end.reset();
  const int read_flags = fcntl(read_end.get(), F_GETFL, 0);
  if (read_flags >= 0) {
    (void)fcntl(read_end.get(), F_SETFL, read_flags | O_NONBLOCK);
  }

  using Clock = std::chrono::steady_clock;

  std::string out;
  out.reserve(4096);
  char buf[4096];
  const auto deadline = Clock::now() + std::chrono::milliseconds(timeout_ms);
  bool child_exited = false;
  bool child_reaped = false;
  int status = 0;

  while (true) {
    while (true) {
      const ssize_t n = read(read_end.get(), buf, sizeof(buf));
      if (n > 0) {
        out.append(buf, static_cast<size_t>(n));
        continue;
      }
      if (n == 0) {
        child_exited = true;
        break;
      }
      if (errno == EINTR) {
        continue;
      }
      if (errno != EAGAIN && errno != EWOULDBLOCK) {
        child_exited = true;
      }
      break;
    }

    const pid_t wait_rc = waitpid(pid, &status, WNOHANG);
    if (wait_rc == pid) {
      child_exited = true;
      child_reaped = true;
    } else if (wait_rc < 0 && errno != EINTR) {
      return {};
    }

    if (child_exited) {
      break;
    }

    const auto now = Clock::now();
    if (now >= deadline) {
      kill(pid, SIGKILL);
      while (waitpid(pid, &status, 0) < 0) {
        if (errno == EINTR) continue;
        break;
      }
      return {};
    }

    const auto remaining =
        std::chrono::duration_cast<std::chrono::milliseconds>(deadline - now).count();
    const int poll_timeout_ms = static_cast<int>(std::min<int64_t>(remaining, 100));
    pollfd pfd{};
    pfd.fd = read_end.get();
    pfd.events = POLLIN | POLLHUP | POLLERR;
    const int poll_rc = poll(&pfd, 1, poll_timeout_ms);
    if (poll_rc < 0 && errno != EINTR) {
      kill(pid, SIGKILL);
      while (waitpid(pid, &status, 0) < 0) {
        if (errno == EINTR) continue;
        break;
      }
      return {};
    }
  }

  read_end.reset();

  if (!child_reaped) {
    while (waitpid(pid, &status, 0) < 0) {
      if (errno == EINTR) continue;
      return {};
    }
  }
  if (!WIFEXITED(status) || WEXITSTATUS(status) != 0) {
    return {};
  }
  return out;
}

}  // namespace probe_core
