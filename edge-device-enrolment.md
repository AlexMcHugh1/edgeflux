# Edge Device Enrolment Guide

This document describes an automated workflow for enrolling a new blank laptop into an edge management ecosystem using a central management server.

---

## 1. Server-Side Setup (Management Server)

Run the following command on your **management server** to create the bootstrap script that edge devices will download during enrollment.

```bash
cat << 'EOF' > ~/bootstrap-edge.sh
#!/bin/sh

# 1. Initialize Network
echo "Starting network..."
udhcpc -i eth0

# 2. Install SSH Client
echo "Installing dependencies..."
apk add openssh-client

# 3. Download Agent from Management Server
echo "Fetching agent binary..."
# Note: -o disables host key verification for automated deployment
scp -o StrictHostKeyChecking=no <USERNAME>@<SERVER_IP>:edge-agent-linux-amd64 .

# 4. Execute Enrollment
echo "Launching Edge Agent..."
chmod +x edge-agent-linux-amd64
SERVER_URL=http://<SERVER_IP>:<PORT> ./edge-agent-linux-amd64
EOF

chmod +x ~/bootstrap-edge.sh
```

Replace the following placeholders with your environment values:

* `<USERNAME>` – SSH user on the management server
* `<SERVER_IP>` – IP address of the management server
* `<PORT>` – Management service port

---

## 2. Edge Device Deployment

Follow these steps on any new device you want to enroll.

### Step A: Booting

* Insert a bootable USB drive with a Linux live environment (e.g., Alpine, Ubuntu, Debian).
* Boot the laptop from the USB device.
* Login as `root` (no password).

### Step B: One-Touch Enrollment

Run the following command to download and execute the bootstrap automation script.

```bash
scp <USERNAME>@<SERVER_IP>:bootstrap-edge.sh . && sh bootstrap-edge.sh
```

This will:

1. Configure networking
2. Install required dependencies
3. Download the enrollment agent
4. Register the device with the management server

---

### Step C: Dashboard Approval

1. Open your management dashboard in a browser:

```
http://<SERVER_IP>:<PORT>
```

2. Locate the newly registered device in the **Pending Devices** list.

3. Click **Approve** to finalise enrollment.
