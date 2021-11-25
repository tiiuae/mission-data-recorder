package internal

/*
#cgo CXXFLAGS: -I /opt/ros/galactic/include -std=c++17
#cgo LDFLAGS: -L /opt/ros/galactic/lib -Wl,-rpath=/opt/ros/galactic/lib
#cgo LDFLAGS: -lrosbag2_cpp -lrosbag2_storage -lrcutils -lrclcpp
#cgo LDFLAGS: -lstd_msgs__rosidl_typesupport_cpp

#include <stdlib.h>

#include "read_rosbag.h"
*/
import "C"
import "unsafe"

type RosbagData struct {
	topic, data string
}

func ReadRosbag(path string) []interface{} {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	cdata := C.readRosbag(cpath)
	defer C.free(unsafe.Pointer(cdata.data))
	cmsgs := (*[1 << 31]C.RosbagMsg)(unsafe.Pointer(cdata.data))
	data := make([]interface{}, cdata.len)
	for i := range data {
		data[i] = RosbagData{
			topic: C.GoString(cmsgs[i].topic),
			data:  C.GoString(cmsgs[i].data),
		}
		C.free(unsafe.Pointer(cmsgs[i].topic))
		C.free(unsafe.Pointer(cmsgs[i].data))
	}
	return data
}
