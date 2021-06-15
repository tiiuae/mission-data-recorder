#ifndef MISSION_DATA_RECORDER_READ_ROSBAG_H
#define MISSION_DATA_RECORDER_READ_ROSBAG_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    char* topic;
    char* data;
} RosbagMsg;

typedef struct {
    RosbagMsg* data;
    size_t len;
} RosbagData;

RosbagData readRosbag(char* path);

#ifdef __cplusplus
}
#endif

#endif
