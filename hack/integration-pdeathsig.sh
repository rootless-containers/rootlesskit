#!/bin/bash
# Test script to verify Pdeathsig behavior using rootlesskit itself
# This script:
# 1. Uses rootlesskit to spawn a long-running process
# 2. Kills the rootlesskit parent process
# 3. Verifies that the child process is killed as expected
# 4. Tests both with --reaper true and --reaper false

source $(realpath $(dirname $0))/common.inc.sh

INFO "Starting Pdeathsig test using rootlesskit..."

# Function to run the test with a specific reaper setting
run_test() {
    local reaper_setting=$1
    INFO "Testing with --reaper $reaper_setting"

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
    INFO "Starting rootlesskit with --reaper $reaper_setting..."
    if [ "$reaper_setting" = "true" ]; then
        $ROOTLESSKIT --reaper $reaper_setting --pidns "$CHILD_SCRIPT" &
    else
        $ROOTLESSKIT --reaper $reaper_setting "$CHILD_SCRIPT" &
    fi
    ROOTLESSKIT_PID=$!
    INFO "Rootlesskit started with PID: $ROOTLESSKIT_PID"

    # Wait a moment for the child to start
    sleep 2

    # Find the child process
    ROOTLESSKIT_CHILD_PID=$(pgrep -P $ROOTLESSKIT_PID)
    if [ -z "$ROOTLESSKIT_CHILD_PID" ]; then
        ERROR "Failed to find rootlesskit child process"
        return 1
    fi
    INFO "Found rootlesskit child process with PID: $ROOTLESSKIT_CHILD_PID"

    # Kill the rootlesskit process
    INFO "Killing rootlesskit process (PID: $ROOTLESSKIT_PID)..."
    kill -9 $ROOTLESSKIT_PID

    # Wait a moment for the rootlesskit child to be killed
    sleep 2

    # Check if the rootlesskit child process is still running
    if ps -p $ROOTLESSKIT_CHILD_PID > /dev/null; then
        ERROR "FAIL: Rootlesskit Child process (PID: $ROOTLESSKIT_CHILD_PID) is still running after rootlesskit parent was killed"
        kill -9 $ROOTLESSKIT_CHILD_PID  # Clean up
        return 1
    else
        INFO "PASS: Rootlesskit Child process (PID: $ROOTLESSKIT_CHILD_PID) was killed as expected"
    fi

    # Check if the marker file exists
    if [ -f "$MARKER_FILE" ]; then
        ERROR "FAIL: Marker file exists, which means the child process wasn't killed by Pdeathsig"
        return 1
    else
        INFO "PASS: Marker file doesn't exist, which means the child process was killed by Pdeathsig"
    fi

    INFO "Test with --reaper $reaper_setting completed successfully!"
    rm -rf "$TEMP_DIR"
    return 0
}

# Run tests with both reaper settings
if ! run_test "true"; then
    ERROR "Test with --reaper true failed"
    exit 1
fi

if ! run_test "false"; then
    ERROR "Test with --reaper false failed"
    exit 1
fi

INFO "All tests completed successfully!"
exit 0
