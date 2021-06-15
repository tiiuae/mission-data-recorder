#include "read_rosbag.h"

#include <cstdlib>
#include <cstring>
#include <vector>

#include <rosbag2_cpp/converter_options.hpp>
#include <rosbag2_cpp/reader.hpp>
#include <rosbag2_cpp/readers/sequential_reader.hpp>
#include <rosbag2_cpp/serialization_format_converter_factory.hpp>
#include <rosbag2_cpp/storage_options.hpp>
#include <rosbag2_cpp/typesupport_helpers.hpp>
#include <std_msgs/msg/string.hpp>

#ifdef __cplusplus
extern "C" {
#endif

RosbagData readRosbag(char* path) {
    auto reader = rosbag2_cpp::Reader(
        std::make_unique<rosbag2_cpp::readers::SequentialReader>()
    );
    auto copts = rosbag2_cpp::ConverterOptions();
    copts.output_serialization_format = "cdr";
    auto sopts = rosbag2_cpp::StorageOptions();
    sopts.uri = path;
    sopts.storage_id = "sqlite3";
    reader.open(sopts, copts);
    rosbag2_cpp::SerializationFormatConverterFactory factory;
    auto deserializer = factory.load_deserializer("cdr");
    auto typeSupportLib = rosbag2_cpp::get_typesupport_library(
        "std_msgs/msg/String",
        "rosidl_typesupport_cpp"
    );
    auto typeSupport = rosbag2_cpp::get_typesupport_handle(
        "std_msgs/msg/String",
        "rosidl_typesupport_cpp",
        typeSupportLib
    );
    std::vector<RosbagMsg> msgs;
    while (reader.has_next()) {
        std_msgs::msg::String msg;
        auto introspect = std::make_shared<rosbag2_cpp::rosbag2_introspection_message_t>();
        introspect->time_stamp = 0;
        introspect->allocator = rcutils_get_default_allocator();
        introspect->message = &msg;

        auto bagMsg = reader.read_next();
        deserializer->deserialize(bagMsg, typeSupport, introspect);
        auto topic = (char*)malloc(sizeof(char) * bagMsg->topic_name.size() + 1);
        strcpy(topic, bagMsg->topic_name.data());
        auto data = (char*)malloc(sizeof(char) * msg.data.size() + 1);
        strcpy(data, msg.data.data());
        msgs.push_back({topic, data});
    }
    auto result = (RosbagMsg*)malloc(sizeof(RosbagMsg) * msgs.size());
    for (size_t i = 0; i < msgs.size(); ++i) {
        result[i] = msgs[i];
    }
    return {result, msgs.size()};
}

#ifdef __cplusplus
}
#endif
