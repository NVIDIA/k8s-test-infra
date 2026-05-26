/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * Minimal umad(4) emulation for ibping (OpenIB vendor ping MAD class 0x32).
 * Sysfs is redirected by shim.c; synthetic umad fds implement libibumad
 * ioctl/read/write/poll. Cross-process server/client uses a global file bus
 * under $MOCK_IB_ROOT/umad-bus/{in,out}.
 */

#define _GNU_SOURCE
#include "umad_mock.h"

#include "mock_ib_root.h"

#include <dlfcn.h>
#include <dirent.h>
#include <endian.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <unistd.h>

/* Match libibumad/umad.h ioctl layout. */
#define IB_IOCTL_MAGIC 0x1b
#define IB_USER_MAD_REGISTER_AGENT _IOWR(IB_IOCTL_MAGIC, 1, struct ib_user_mad_reg_req)
#define IB_USER_MAD_UNREGISTER_AGENT _IOW(IB_IOCTL_MAGIC, 2, uint32_t)
#define IB_USER_MAD_ENABLE_PKEY _IO(IB_IOCTL_MAGIC, 3)
#define IB_USER_MAD_REGISTER_AGENT2 _IOWR(IB_IOCTL_MAGIC, 4, struct ib_user_mad_reg_req2)

struct ib_user_mad_reg_req {
	uint32_t id;
	uint32_t method_mask[4];
	uint8_t qpn;
	uint8_t mgmt_class;
	uint8_t mgmt_class_version;
	uint8_t oui[3];
	uint8_t rmpp_version;
};

struct ib_user_mad_reg_req2 {
	uint32_t id;
	uint32_t qpn;
	uint8_t mgmt_class;
	uint8_t mgmt_class_version;
	uint16_t res;
	uint32_t flags;
	uint64_t method_mask[2];
	uint32_t oui;
	uint8_t rmpp_version;
	uint8_t reserved[3];
};

typedef struct ib_mad_addr {
	uint32_t qpn;
	uint32_t qkey;
	uint16_t lid;
	uint8_t sl;
	uint8_t path_bits;
	uint8_t grh_present;
	uint8_t gid_index;
	uint8_t hop_limit;
	uint8_t traffic_class;
	uint8_t gid[16];
	uint32_t flow_label;
	uint16_t pkey_index;
	uint8_t reserved[6];
} ib_mad_addr_t;

typedef struct ib_user_mad_hdr {
	uint32_t agent_id;
	uint32_t status;
	uint32_t timeout_ms;
	uint32_t retries;
	uint32_t length;
	ib_mad_addr_t addr;
} ib_user_mad_hdr_t;

#define UMAD_HDR_SIZE sizeof(ib_user_mad_hdr_t)
#define UMAD_MAX_PKT (UMAD_HDR_SIZE + 256)

#define IB_VENDOR_OPENIB_PING_CLASS 0x32
#define IB_MAD_METHOD_GET 0x01
#define IB_MAD_METHOD_GET_RESP 0x81
#define IB_VENDOR_RANGE2_DATA_OFFS 40

struct mad_hdr_simple {
	uint8_t base_version;
	uint8_t mgmt_class;
	uint8_t class_version;
	uint8_t method;
	uint16_t status;
	uint16_t class_specific;
	uint64_t tid;
	uint16_t attr_id;
	uint16_t resv;
	uint32_t attr_mod;
} __attribute__((packed));

#define MAX_UMAD_MOCK_FDS 32
#define MAX_AGENTS 8
#define MAX_RX_QUEUE 8

typedef struct {
	int active;
	int fd;
	int umad_idx;
	int local_lid;
	int pkey_enabled;
	int next_agent_id;
	int agents[MAX_AGENTS];
	int agent_count;
	uint64_t pending_tid;
	int awaiting_resp;
	uint8_t rx_queue[MAX_RX_QUEUE][UMAD_MAX_PKT];
	size_t rx_len[MAX_RX_QUEUE];
	int rx_head;
	int rx_tail;
	pthread_mutex_t lock;
} umad_slot_t;

static umad_slot_t g_slots[MAX_UMAD_MOCK_FDS];
static pthread_mutex_t g_table_lock = PTHREAD_MUTEX_INITIALIZER;

static umad_slot_t *slot_for_fd(int fd) {
	for (int i = 0; i < MAX_UMAD_MOCK_FDS; i++) {
		if (g_slots[i].active && g_slots[i].fd == fd)
			return &g_slots[i];
	}
	return NULL;
}

static int alloc_slot(int umad_idx, int fd, int local_lid) {
	for (int i = 0; i < MAX_UMAD_MOCK_FDS; i++) {
		if (!g_slots[i].active) {
			memset(&g_slots[i], 0, sizeof(g_slots[i]));
			g_slots[i].active = 1;
			g_slots[i].fd = fd;
			g_slots[i].umad_idx = umad_idx;
			g_slots[i].local_lid = local_lid;
			g_slots[i].next_agent_id = 1;
			pthread_mutex_init(&g_slots[i].lock, NULL);
			return i;
		}
	}
	return -1;
}

static void bus_mkdir_p(const char *path, mode_t mode) {
	char tmp[PATH_MAX];
	char *p;

	snprintf(tmp, sizeof(tmp), "%s", path);
	for (p = tmp + 1; *p; p++) {
		if (*p != '/')
			continue;
		*p = '\0';
		mkdir(tmp, mode);
		*p = '/';
	}
	mkdir(tmp, mode);
}

static void ensure_bus_dirs(void) {
	const char *root = mock_ib_root();
	char path[PATH_MAX];

	snprintf(path, sizeof(path), "%s/umad-bus", root);
	bus_mkdir_p(path, 0700);
	snprintf(path, sizeof(path), "%s/umad-bus/in", root);
	bus_mkdir_p(path, 0700);
	snprintf(path, sizeof(path), "%s/umad-bus/out", root);
	bus_mkdir_p(path, 0700);
}

static int read_port_lid(int umad_idx, int port) {
	char path[PATH_MAX];
	char ibdev[32];
	char buf[32];
	FILE *f;

	snprintf(path, sizeof(path), "%s/sys/class/infiniband_mad/umad%d/ibdev",
		 mock_ib_root(), umad_idx);
	f = fopen(path, "r");
	if (!f)
		return umad_idx + 1;
	if (!fgets(ibdev, sizeof(ibdev), f)) {
		fclose(f);
		return umad_idx + 1;
	}
	fclose(f);
	ibdev[strcspn(ibdev, "\n")] = '\0';

	snprintf(path, sizeof(path), "%s/sys/class/infiniband/%s/ports/%d/lid",
		 mock_ib_root(), ibdev, port);
	f = fopen(path, "r");
	if (!f)
		return umad_idx + 1;
	if (!fgets(buf, sizeof(buf), f)) {
		fclose(f);
		return umad_idx + 1;
	}
	fclose(f);
	return (int)strtol(buf, NULL, 0);
}

static int read_local_lid_for_umad(int umad_idx) {
	char path[PATH_MAX];
	char buf[32];
	int port = 1;
	FILE *f;

	snprintf(path, sizeof(path), "%s/sys/class/infiniband_mad/umad%d/port",
		 mock_ib_root(), umad_idx);
	f = fopen(path, "r");
	if (f && fgets(buf, sizeof(buf), f))
		port = atoi(buf);
	if (f)
		fclose(f);
	return read_port_lid(umad_idx, port);
}

static void rx_push(umad_slot_t *slot, const void *buf, size_t len) {
	int next = (slot->rx_tail + 1) % MAX_RX_QUEUE;

	if (next == slot->rx_head)
		return;
	if (len > UMAD_MAX_PKT)
		len = UMAD_MAX_PKT;
	memcpy(slot->rx_queue[slot->rx_tail], buf, len);
	slot->rx_len[slot->rx_tail] = len;
	slot->rx_tail = next;
}

static int rx_pop(umad_slot_t *slot, void *buf, size_t count) {
	size_t len;

	if (slot->rx_head == slot->rx_tail) {
		errno = EAGAIN;
		return -1;
	}
	len = slot->rx_len[slot->rx_head];
	if (len > count)
		len = count;
	memcpy(buf, slot->rx_queue[slot->rx_head], len);
	slot->rx_head = (slot->rx_head + 1) % MAX_RX_QUEUE;
	return (int)len;
}

static int rx_pending(const umad_slot_t *slot) {
	return slot->rx_head != slot->rx_tail;
}

static int bus_has_in(void) {
	char dir[PATH_MAX];
	DIR *d;
	struct dirent *de;

	snprintf(dir, sizeof(dir), "%s/umad-bus/in", mock_ib_root());
	d = opendir(dir);
	if (!d)
		return 0;
	while ((de = readdir(d)) != NULL) {
		if (de->d_name[0] != '.') {
			closedir(d);
			return 1;
		}
	}
	closedir(d);
	return 0;
}

static int bus_has_out_tid(uint64_t tid) {
	char path[PATH_MAX];
	struct stat st;

	snprintf(path, sizeof(path), "%s/umad-bus/out/%016llx.mad",
		 mock_ib_root(), (unsigned long long)tid);
	return stat(path, &st) == 0 && S_ISREG(st.st_mode);
}

static void fill_hostname(char *dest, size_t dest_sz) {
	char host[128];
	char domain[128];
	size_t n;

	if (gethostname(host, sizeof(host)) != 0)
		snprintf(host, sizeof(host), "mockib");
	host[sizeof(host) - 1] = '\0';
	if (getdomainname(domain, sizeof(domain)) != 0 || domain[0] == '\0') {
		snprintf(dest, dest_sz, "%s", host);
		return;
	}
	domain[sizeof(domain) - 1] = '\0';
	n = snprintf(dest, dest_sz, "%s.%s", host, domain);
	if (n >= dest_sz)
		dest[dest_sz - 1] = '\0';
}

static int build_ping_response(const uint8_t *req_pkt, size_t req_len,
			       uint8_t *resp_pkt, size_t resp_sz) {
	const struct mad_hdr_simple *req_mad;
	struct mad_hdr_simple *resp_mad;
	size_t need;
	char hostdom[256];

	if (req_len < UMAD_HDR_SIZE + sizeof(struct mad_hdr_simple))
		return -1;
	req_mad = (const struct mad_hdr_simple *)(req_pkt + UMAD_HDR_SIZE);
	if (req_mad->mgmt_class != IB_VENDOR_OPENIB_PING_CLASS ||
	    req_mad->method != IB_MAD_METHOD_GET)
		return -1;

	need = UMAD_HDR_SIZE + sizeof(struct mad_hdr_simple) + IB_VENDOR_RANGE2_DATA_OFFS + 64;
	if (resp_sz < need)
		return -1;

	memset(resp_pkt, 0, need);
	memcpy(resp_pkt, req_pkt, UMAD_HDR_SIZE);
	{
		ib_user_mad_hdr_t *rh = (ib_user_mad_hdr_t *)resp_pkt;
		rh->status = 0;
		rh->length = (uint32_t)(need - UMAD_HDR_SIZE);
	}
	memcpy(resp_pkt + UMAD_HDR_SIZE, req_mad, sizeof(*req_mad));
	resp_mad = (struct mad_hdr_simple *)(resp_pkt + UMAD_HDR_SIZE);
	resp_mad->method = IB_MAD_METHOD_GET_RESP;
	resp_mad->status = 0;
	fill_hostname(hostdom, sizeof(hostdom));
	memcpy(resp_pkt + UMAD_HDR_SIZE + IB_VENDOR_RANGE2_DATA_OFFS, hostdom,
	       strlen(hostdom) + 1);
	return (int)need;
}

static void bus_publish_in(const void *buf, size_t len) {
	const char *root = mock_ib_root();
	char dir[PATH_MAX];
	char path[PATH_MAX];
	char tmp[PATH_MAX];
	int fd;
	static unsigned seq;

	ensure_bus_dirs();
	snprintf(dir, sizeof(dir), "%s/umad-bus/in", root);
	snprintf(tmp, sizeof(tmp), "%s/%u.%d.tmp", dir, (unsigned)getpid(), seq++);
	fd = open(tmp, O_WRONLY | O_CREAT | O_TRUNC, 0600);
	if (fd < 0)
		return;
	if (write(fd, buf, len) != (ssize_t)len) {
		close(fd);
		unlink(tmp);
		return;
	}
	close(fd);
	snprintf(path, sizeof(path), "%s/%s", dir, strrchr(tmp, '/') + 1);
	rename(tmp, path);
}

static int bus_take_in(void *buf, size_t count) {
	const char *root = mock_ib_root();
	char dir[PATH_MAX];
	DIR *d;
	struct dirent *de;
	char path[PATH_MAX];
	int fd, n;
	struct stat st;
	char oldest[PATH_MAX];
	time_t oldest_mtime = 0;
	int found = 0;

	snprintf(dir, sizeof(dir), "%s/umad-bus/in", root);
	d = opendir(dir);
	if (!d)
		return -1;
	oldest[0] = '\0';
	while ((de = readdir(d)) != NULL) {
		if (de->d_name[0] == '.')
			continue;
		snprintf(path, sizeof(path), "%s/%s", dir, de->d_name);
		if (stat(path, &st) != 0 || !S_ISREG(st.st_mode))
			continue;
		if (!found || st.st_mtime <= oldest_mtime) {
			oldest_mtime = st.st_mtime;
			strncpy(oldest, path, sizeof(oldest) - 1);
			oldest[sizeof(oldest) - 1] = '\0';
			found = 1;
		}
	}
	closedir(d);
	if (!found) {
		errno = EAGAIN;
		return -1;
	}
	fd = open(oldest, O_RDONLY);
	if (fd < 0)
		return -1;
	n = (int)read(fd, buf, count);
	close(fd);
	unlink(oldest);
	return n;
}

static void bus_publish_out(uint64_t tid, const void *buf, size_t len) {
	const char *root = mock_ib_root();
	char path[PATH_MAX];
	char tmp[PATH_MAX];
	int fd;

	ensure_bus_dirs();
	snprintf(path, sizeof(path), "%s/umad-bus/out/%016llx.mad", root,
		 (unsigned long long)tid);
	snprintf(tmp, sizeof(tmp), "%s.tmp", path);
	fd = open(tmp, O_WRONLY | O_CREAT | O_TRUNC, 0600);
	if (fd < 0)
		return;
	if (write(fd, buf, len) == (ssize_t)len)
		rename(tmp, path);
	else
		unlink(tmp);
	close(fd);
}

static int bus_take_out(uint64_t tid, void *buf, size_t count) {
	const char *root = mock_ib_root();
	char path[PATH_MAX];
	int fd, n;

	snprintf(path, sizeof(path), "%s/umad-bus/out/%016llx.mad", root,
		 (unsigned long long)tid);
	fd = open(path, O_RDONLY);
	if (fd < 0) {
		errno = EAGAIN;
		return -1;
	}
	n = (int)read(fd, buf, count);
	close(fd);
	unlink(path);
	return n;
}

static int dest_lid_from_pkt(const uint8_t *pkt) {
	const ib_user_mad_hdr_t *hdr = (const ib_user_mad_hdr_t *)pkt;
	return (int)be16toh(hdr->addr.lid);
}

static uint64_t mad_tid_from_pkt(const uint8_t *pkt) {
	const struct mad_hdr_simple *mad;

	if (UMAD_HDR_SIZE + sizeof(*mad) > UMAD_MAX_PKT)
		return 0;
	mad = (const struct mad_hdr_simple *)(pkt + UMAD_HDR_SIZE);
	return mad->tid;
}

static int mock_fd_poll_ready(umad_slot_t *slot) {
	if (rx_pending(slot))
		return 1;
	if (bus_has_in())
		return 1;
	if (slot->awaiting_resp && slot->pending_tid &&
	    bus_has_out_tid(slot->pending_tid))
		return 1;
	return 0;
}

int umad_mock_open(const char *resolved_path) {
	const char *needle = "/dev/infiniband/umad";
	const char *p;
	int idx;
	int fd;
	int local_lid;

	p = strstr(resolved_path, needle);
	if (!p)
		return -1;
	p += strlen(needle);
	idx = (int)strtol(p, NULL, 10);
	if (idx < 0 || idx > 255)
		return -1;

	ensure_bus_dirs();
	local_lid = read_local_lid_for_umad(idx);

	fd = open("/dev/null", O_RDWR | O_CLOEXEC);
	if (fd < 0)
		return -1;

	pthread_mutex_lock(&g_table_lock);
	if (alloc_slot(idx, fd, local_lid) < 0) {
		pthread_mutex_unlock(&g_table_lock);
		close(fd);
		errno = EMFILE;
		return -1;
	}
	pthread_mutex_unlock(&g_table_lock);
	return fd;
}

int umad_mock_is_mock_fd(int fd) {
	return slot_for_fd(fd) != NULL;
}

int umad_mock_ioctl(int fd, unsigned long request, void *arg) {
	umad_slot_t *slot = slot_for_fd(fd);
	struct ib_user_mad_reg_req *req;
	struct ib_user_mad_reg_req2 *req2;

	if (!slot) {
		errno = ENOTTY;
		return -1;
	}

	pthread_mutex_lock(&slot->lock);
	if (request == IB_USER_MAD_ENABLE_PKEY) {
		slot->pkey_enabled = 1;
		pthread_mutex_unlock(&slot->lock);
		return 0;
	}
	if (request == IB_USER_MAD_REGISTER_AGENT ||
	    request == IB_USER_MAD_REGISTER_AGENT2) {
		if (!arg) {
			pthread_mutex_unlock(&slot->lock);
			errno = EINVAL;
			return -1;
		}
		if (slot->agent_count >= MAX_AGENTS) {
			pthread_mutex_unlock(&slot->lock);
			errno = ENOMEM;
			return -1;
		}
		{
			uint32_t agent_id = (uint32_t)slot->next_agent_id++;
			if (request == IB_USER_MAD_REGISTER_AGENT2) {
				req2 = arg;
				req2->id = agent_id;
			} else {
				req = arg;
				req->id = agent_id;
			}
			slot->agents[slot->agent_count++] = (int)agent_id;
		}
		pthread_mutex_unlock(&slot->lock);
		return 0;
	}
	if (request == IB_USER_MAD_UNREGISTER_AGENT) {
		pthread_mutex_unlock(&slot->lock);
		return 0;
	}
	pthread_mutex_unlock(&slot->lock);
	errno = ENOTTY;
	return -1;
}

ssize_t umad_mock_write(int fd, const void *buf, size_t count) {
	umad_slot_t *slot = slot_for_fd(fd);
	uint8_t resp[UMAD_MAX_PKT];
	int resp_len;
	int dest_lid;
	uint64_t tid;
	const uint8_t *pkt = buf;
	struct mad_hdr_simple *mad;

	if (!slot || count < UMAD_HDR_SIZE) {
		errno = EINVAL;
		return -1;
	}

	pthread_mutex_lock(&slot->lock);

	if (count >= UMAD_HDR_SIZE + sizeof(struct mad_hdr_simple)) {
		mad = (struct mad_hdr_simple *)(pkt + UMAD_HDR_SIZE);
		if (mad->mgmt_class == IB_VENDOR_OPENIB_PING_CLASS &&
		    mad->method == IB_MAD_METHOD_GET_RESP) {
			tid = mad->tid;
			bus_publish_out(tid, pkt, count);
			pthread_mutex_unlock(&slot->lock);
			return (ssize_t)count;
		}
	}

	dest_lid = dest_lid_from_pkt(pkt);
	tid = mad_tid_from_pkt(pkt);
	resp_len = build_ping_response(pkt, count, resp, sizeof(resp));

	if (resp_len > 0 && dest_lid == slot->local_lid) {
		rx_push(slot, resp, (size_t)resp_len);
		slot->awaiting_resp = 0;
		slot->pending_tid = 0;
	} else if (resp_len > 0) {
		bus_publish_in(pkt, count);
		slot->pending_tid = tid;
		slot->awaiting_resp = 1;
	}

	pthread_mutex_unlock(&slot->lock);
	return (ssize_t)count;
}

ssize_t umad_mock_read(int fd, void *buf, size_t count) {
	umad_slot_t *slot = slot_for_fd(fd);
	int n;

	if (!slot) {
		errno = EBADF;
		return -1;
	}

	pthread_mutex_lock(&slot->lock);

	n = rx_pop(slot, buf, count);
	if (n >= 0) {
		pthread_mutex_unlock(&slot->lock);
		return n;
	}

	if (slot->awaiting_resp && slot->pending_tid) {
		uint64_t tid = slot->pending_tid;
		pthread_mutex_unlock(&slot->lock);
		n = bus_take_out(tid, buf, count);
		if (n >= 0) {
			pthread_mutex_lock(&slot->lock);
			slot->awaiting_resp = 0;
			slot->pending_tid = 0;
			pthread_mutex_unlock(&slot->lock);
			return n;
		}
		pthread_mutex_lock(&slot->lock);
	}

	n = bus_take_in(buf, count);
	if (n >= 0) {
		pthread_mutex_unlock(&slot->lock);
		return n;
	}

	pthread_mutex_unlock(&slot->lock);
	errno = EAGAIN;
	return -1;
}

int umad_mock_close(int fd) {
	umad_slot_t *slot = slot_for_fd(fd);

	if (slot) {
		pthread_mutex_lock(&g_table_lock);
		slot->active = 0;
		pthread_mutex_unlock(&g_table_lock);
		pthread_mutex_destroy(&slot->lock);
	}
	return 0;
}

static int (*real_poll_fn)(struct pollfd *, nfds_t, int);

static int call_real_poll(struct pollfd *fds, nfds_t nfds, int timeout) {
	if (!real_poll_fn)
		real_poll_fn = (int (*)(struct pollfd *, nfds_t, int))dlsym(RTLD_NEXT, "poll");
	if (!real_poll_fn) {
		errno = ENOSYS;
		return -1;
	}
	return real_poll_fn(fds, nfds, timeout);
}

int umad_mock_poll(struct pollfd *fds, nfds_t nfds, int timeout) {
	int ready = 0;
	struct timespec start, now;
	long elapsed_ms = 0;
	struct pollfd real_fds[64];
	int real_map[64];
	nfds_t real_n = 0;

	if (nfds > 64)
		nfds = 64;

	if (!real_poll_fn)
		real_poll_fn = (int (*)(struct pollfd *, nfds_t, int))dlsym(RTLD_NEXT, "poll");

	clock_gettime(CLOCK_MONOTONIC, &start);

	for (;;) {
		ready = 0;
		real_n = 0;
		for (nfds_t i = 0; i < nfds; i++) {
			umad_slot_t *slot = slot_for_fd(fds[i].fd);
			fds[i].revents = 0;
			if (!slot) {
				if (real_n < 64) {
					real_map[real_n] = (int)i;
					real_fds[real_n] = fds[i];
					real_n++;
				}
				continue;
			}
			if (!(fds[i].events & POLLIN))
				continue;
			pthread_mutex_lock(&slot->lock);
			if (mock_fd_poll_ready(slot)) {
				fds[i].revents = POLLIN;
				ready++;
			}
			pthread_mutex_unlock(&slot->lock);
		}

		if (real_n > 0) {
			int rem = timeout;
			if (timeout > 0) {
				clock_gettime(CLOCK_MONOTONIC, &now);
				elapsed_ms = (now.tv_sec - start.tv_sec) * 1000L +
					     (now.tv_nsec - start.tv_nsec) / 1000000L;
				rem = timeout - (int)elapsed_ms;
				if (rem < 0)
					rem = 0;
			}
			int rn = call_real_poll(real_fds, real_n, ready > 0 ? 0 : rem);
			if (rn > 0) {
				for (nfds_t j = 0; j < real_n; j++) {
					if (real_fds[j].revents) {
						fds[real_map[j]].revents = real_fds[j].revents;
						ready++;
					}
				}
			}
		}

		if (ready > 0)
			return ready;
		if (timeout == 0)
			return 0;
		if (timeout > 0) {
			clock_gettime(CLOCK_MONOTONIC, &now);
			elapsed_ms = (now.tv_sec - start.tv_sec) * 1000L +
				     (now.tv_nsec - start.tv_nsec) / 1000000L;
			if (elapsed_ms >= timeout)
				return 0;
		}
		usleep(1000);
	}
}
