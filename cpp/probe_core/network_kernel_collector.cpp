#include "network_kernel_collector.h"

#include <arpa/inet.h>
#include <linux/if_link.h>
#include <linux/inet_diag.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <linux/sock_diag.h>
#include <netinet/tcp.h>
#include <sys/socket.h>

#include <cstring>
#include <string>

#include "posix_raii.h"

namespace probe_core {
namespace {

bool startsWith(const std::string& s, const std::string& prefix) {
  return s.size() >= prefix.size() && s.compare(0, prefix.size(), prefix) == 0;
}

namespace {
struct DiagRequest {
  nlmsghdr nlh;
  inet_diag_req_v2 req;
};
}  // namespace

void collectSocketQueuesForFamily(int family, SocketQueueMap* out) {
  if (out == nullptr) return;

  ScopedFD fd(socket(AF_NETLINK, SOCK_DGRAM | SOCK_CLOEXEC, NETLINK_SOCK_DIAG));
  if (!fd.valid()) return;

  sockaddr_nl addr{};
  addr.nl_family = AF_NETLINK;
  if (bind(fd.get(), reinterpret_cast<const sockaddr*>(&addr), sizeof(addr)) < 0) {
    return;
  }

  DiagRequest request{};
  request.nlh.nlmsg_len = sizeof(request);
  request.nlh.nlmsg_type = SOCK_DIAG_BY_FAMILY;
  request.nlh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
  request.nlh.nlmsg_seq = 1;
  request.req.sdiag_family = static_cast<__u8>(family);
  request.req.sdiag_protocol = IPPROTO_TCP;
  request.req.idiag_ext = 0;
  request.req.idiag_states = UINT32_MAX;

  if (send(fd.get(), &request, sizeof(request), 0) < 0) {
    return;
  }

  char buffer[8192];
  while (true) {
    const ssize_t nread = recv(fd.get(), buffer, sizeof(buffer), 0);
    if (nread <= 0) {
      return;
    }

    ssize_t remaining = nread;
    for (nlmsghdr* header = reinterpret_cast<nlmsghdr*>(buffer); NLMSG_OK(header, remaining);
         header = NLMSG_NEXT(header, remaining)) {
      if (header->nlmsg_type == NLMSG_DONE) {
        return;
      }
      if (header->nlmsg_type == NLMSG_ERROR) {
        return;
      }
      if (header->nlmsg_type != SOCK_DIAG_BY_FAMILY) {
        continue;
      }
      const auto* message =
          reinterpret_cast<const inet_diag_msg*>(NLMSG_DATA(header));
      if (message == nullptr || message->idiag_inode == 0) {
        continue;
      }
      (*out)[message->idiag_inode] =
          SocketQueue{message->idiag_wqueue, message->idiag_rqueue};
    }
  }
}

}  // namespace

NetlinkLinkData readNetlinkLinkData() {
  NetlinkLinkData out;
  ScopedFD fd(socket(AF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE));
  if (!fd.valid()) return out;

  struct {
    nlmsghdr nlh;
    ifinfomsg ifm;
  } req{};
  req.nlh.nlmsg_len = NLMSG_LENGTH(sizeof(ifinfomsg));
  req.nlh.nlmsg_type = RTM_GETLINK;
  req.nlh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
  req.nlh.nlmsg_seq = 1;
  req.ifm.ifi_family = AF_UNSPEC;

  sockaddr_nl addr{};
  addr.nl_family = AF_NETLINK;
  if (sendto(fd.get(), &req, req.nlh.nlmsg_len, 0, reinterpret_cast<sockaddr*>(&addr),
             sizeof(addr)) < 0) {
    return out;
  }

  char buf[8192];
  while (true) {
    const ssize_t nread = recv(fd.get(), buf, sizeof(buf), 0);
    if (nread <= 0) break;

    ssize_t rem = nread;
    for (nlmsghdr* nh = reinterpret_cast<nlmsghdr*>(buf); NLMSG_OK(nh, rem);
         nh = NLMSG_NEXT(nh, rem)) {
      if (nh->nlmsg_type == NLMSG_DONE) {
        out.ok = true;
        return out;
      }
      if (nh->nlmsg_type == NLMSG_ERROR) {
        return NetlinkLinkData{};
      }
      if (nh->nlmsg_type != RTM_NEWLINK) continue;

      auto* ifi = reinterpret_cast<ifinfomsg*>(NLMSG_DATA(nh));
      int len = IFLA_PAYLOAD(nh);
      std::string ifname;
      NetSnapshot net{};
      bool have_stats = false;
      uint64_t txqlen = 0;
      bool have_txqlen = false;

      for (rtattr* attr = IFLA_RTA(ifi); RTA_OK(attr, len); attr = RTA_NEXT(attr, len)) {
        if (attr->rta_type == IFLA_IFNAME) {
          ifname = reinterpret_cast<const char*>(RTA_DATA(attr));
          continue;
        }
        if (attr->rta_type == IFLA_TXQLEN && RTA_PAYLOAD(attr) >= sizeof(uint32_t)) {
          uint32_t raw = 0;
          memcpy(&raw, RTA_DATA(attr), sizeof(raw));
          txqlen = raw;
          have_txqlen = true;
          continue;
        }
        if (attr->rta_type == IFLA_STATS64 && RTA_PAYLOAD(attr) >= sizeof(rtnl_link_stats64)) {
          rtnl_link_stats64 stats{};
          memcpy(&stats, RTA_DATA(attr), sizeof(stats));
          net.rx_bytes = stats.rx_bytes;
          net.rx_packets = stats.rx_packets;
          net.rx_errs = stats.rx_errors;
          net.rx_drop = stats.rx_dropped;
          net.tx_bytes = stats.tx_bytes;
          net.tx_packets = stats.tx_packets;
          net.tx_errs = stats.tx_errors;
          net.tx_drop = stats.tx_dropped;
          have_stats = true;
          continue;
        }
        if (attr->rta_type == IFLA_STATS && RTA_PAYLOAD(attr) >= sizeof(rtnl_link_stats) &&
            !have_stats) {
          rtnl_link_stats stats{};
          memcpy(&stats, RTA_DATA(attr), sizeof(stats));
          net.rx_bytes = stats.rx_bytes;
          net.rx_packets = stats.rx_packets;
          net.rx_errs = stats.rx_errors;
          net.rx_drop = stats.rx_dropped;
          net.tx_bytes = stats.tx_bytes;
          net.tx_packets = stats.tx_packets;
          net.tx_errs = stats.tx_errors;
          net.tx_drop = stats.tx_dropped;
          have_stats = true;
        }
      }

      if (ifname.empty() || startsWith(ifname, "lo")) {
        continue;
      }
      if (have_stats) {
        out.stats[ifname] = net;
      }
      if (have_txqlen) {
        out.tx_queue_len[ifname] = txqlen;
      }
    }
  }

  out.ok = !out.stats.empty() || !out.tx_queue_len.empty();
  return out;
}

SocketQueueMap readSocketQueuesByInode() {
  SocketQueueMap out;
  collectSocketQueuesForFamily(AF_INET, &out);
  collectSocketQueuesForFamily(AF_INET6, &out);
  return out;
}

}  // namespace probe_core
