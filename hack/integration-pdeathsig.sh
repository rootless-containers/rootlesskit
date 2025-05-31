#!/bin/bash
# Test script to verify Pdeathsig behavior using rootlesskit itself
# This script:
# 1. Uses rootlesskit to spawn a long-running process
# 2. Kills the rootlesskit parent process
# 3. Verifies that the child process is killed as expected

source $(realpath $(dirname $0))/common.inc.sh

INFO "Starting Pdeathsig test using rootlesskit..."

# Create a temporary directory for test artifacts
TEMP_DIR=$(mktemp -d)
INFO "Created temporary directory: $TEMP_DIR"

# Create a marker file that will be touched by the child process if it's still alive
MARKER_FILE="$TEMP_DIR/child_still_alive"

# Create a script that will be executed by rootlesskit
CHILD_SCRIPT="$TEMP_DIR/child_script.sh"
cat > "$CHILD_SCRIPT" << 'EOF'
#!/bin/bash
echo "Child process started with PID: $$"
echo "Parent PID: $PPID"

# Register a trap to handle signals
trap 'echo "Child received signal, exiting"; exit 1' TERM INT

# Run for 30 seconds, checking if parent is still alive
for i in {1..30}; do
    echo "Child still running (iteration $i)..."

    # Check if parent has changed (died)
    CURRENT_PPID=$(ps -o ppid= -p $$)
    if [ "$CURRENT_PPID" != "$PPID" ]; then
        echo "Parent changed from $PPID to $CURRENT_PPID"
        if [ "$CURRENT_PPID" = "1" ]; then
            echo "Parent is now init (PID 1), parent has died"
            echo "Child should be killed by Pdeathsig, but if you see this message, it wasn't"
            touch MARKER_FILE_PLACEHOLDER
            exit 1
        fi
    fi

    sleep 1
done

# If we reach here, the child wasn't killed
echo "Child completed normally (this shouldn't happen if Pdeathsig is working)"
touch MARKER_FILE_PLACEHOLDER
EOF

# Replace the placeholder with the actual marker file path
sed -i "s|MARKER_FILE_PLACEHOLDER|$MARKER_FILE|g" "$CHILD_SCRIPT"
chmod +x "$CHILD_SCRIPT"

# Start rootlesskit with the child script
INFO "Starting rootlesskit..."
$ROOTLESSKIT "$CHILD_SCRIPT" &
ROOTLESSKIT_PID=$!
INFO "Rootlesskit started with PID: $ROOTLESSKIT_PID"

# Wait a moment for the child to start
sleep 2

# Find the child process
CHILD_PID=$(pgrep -P $ROOTLESSKIT_PID)
if [ -z "$CHILD_PID" ]; then
    ERROR "Failed to find child process"
    exit 1
fi
INFO "Found child process with PID: $CHILD_PID"

# Kill the rootlesskit process
INFO "Killing rootlesskit process (PID: $ROOTLESSKIT_PID)..."
kill -9 $ROOTLESSKIT_PID

# Wait a moment for the child to be killed
sleep 2

# Check if the child process is still running
if ps -p $CHILD_PID > /dev/null; then
    ERROR "FAIL: Child process (PID: $CHILD_PID) is still running after parent was killed"
    kill -9 $CHILD_PID  # Clean up
    exit 1
else
    INFO "PASS: Child process (PID: $CHILD_PID) was killed as expected"
fi

# Check if the marker file exists
if [ -f "$MARKER_FILE" ]; then
    ERROR "FAIL: Marker file exists, which means the child process wasn't killed by Pdeathsig"
    exit 1
else
    INFO "PASS: Marker file doesn't exist, which means the child process was killed by Pdeathsig"
fi

INFO "Test completed successfully!"
rm -rf "$TEMP_DIR"
exit 0
