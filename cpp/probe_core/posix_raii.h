#ifndef AI_SRE_AGENT_PROBE_CORE_POSIX_RAII_H_
#define AI_SRE_AGENT_PROBE_CORE_POSIX_RAII_H_

#include <dirent.h>
#include <unistd.h>

namespace probe_core {

class ScopedFD {
 public:
  ScopedFD() = default;
  explicit ScopedFD(int fd) : fd_(fd) {}
  ~ScopedFD() { reset(); }

  ScopedFD(const ScopedFD&) = delete;
  ScopedFD& operator=(const ScopedFD&) = delete;

  ScopedFD(ScopedFD&& other) noexcept : fd_(other.release()) {}
  ScopedFD& operator=(ScopedFD&& other) noexcept {
    if (this != &other) {
      reset(other.release());
    }
    return *this;
  }

  bool valid() const { return fd_ >= 0; }
  int get() const { return fd_; }

  int release() {
    const int out = fd_;
    fd_ = -1;
    return out;
  }

  void reset(int next = -1) {
    if (fd_ >= 0) {
      close(fd_);
    }
    fd_ = next;
  }

 private:
  int fd_ = -1;
};

class ScopedDir {
 public:
  ScopedDir() = default;
  explicit ScopedDir(DIR* dir) : dir_(dir) {}
  ~ScopedDir() { reset(); }

  ScopedDir(const ScopedDir&) = delete;
  ScopedDir& operator=(const ScopedDir&) = delete;

  ScopedDir(ScopedDir&& other) noexcept : dir_(other.release()) {}
  ScopedDir& operator=(ScopedDir&& other) noexcept {
    if (this != &other) {
      reset(other.release());
    }
    return *this;
  }

  bool valid() const { return dir_ != nullptr; }
  DIR* get() const { return dir_; }

  DIR* release() {
    DIR* out = dir_;
    dir_ = nullptr;
    return out;
  }

  void reset(DIR* next = nullptr) {
    if (dir_ != nullptr) {
      closedir(dir_);
    }
    dir_ = next;
  }

 private:
  DIR* dir_ = nullptr;
};

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_POSIX_RAII_H_
